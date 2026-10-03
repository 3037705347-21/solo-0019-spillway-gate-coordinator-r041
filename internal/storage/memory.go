package storage

import (
	"context"
	"fmt"
	"sync"
	"time"

	"example.com/spillway-gate-coordinator/internal/domain"
)

type MemoryRepository struct {
	mu       sync.RWMutex
	gates    map[string]domain.Gate
	commands map[string]domain.GateCommand
	plans    map[string]domain.OperationPlan
}

func NewMemoryRepository() *MemoryRepository {
	return &MemoryRepository{
		gates:    make(map[string]domain.Gate),
		commands: make(map[string]domain.GateCommand),
		plans:    make(map[string]domain.OperationPlan),
	}
}

func (m *MemoryRepository) CreateGate(_ context.Context, gate domain.Gate) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.gates[gate.ID]; exists {
		return domain.Conflict("duplicate_gate_id", "gate id already exists")
	}
	m.gates[gate.ID] = gate
	return nil
}

func (m *MemoryRepository) Gate(_ context.Context, id string) (domain.Gate, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	gate, exists := m.gates[id]
	if !exists {
		return domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	return gate, nil
}

func (m *MemoryRepository) CreateCommand(_ context.Context, command domain.GateCommand) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.NotFound("gate_not_found", "gate id was not found")
	}
	if gate.Status == domain.GateFaulted {
		return domain.Conflict("gate_faulted", "faulted gates cannot receive new commands")
	}
	if command.StartingAperture != gate.Aperture {
		return domain.Conflict("gate_aperture_changed", "gate aperture changed before the command was stored")
	}
	if command.TargetAperture > gate.MaxAperture {
		return domain.Invalid("invalid_target_aperture", "target aperture is outside the gate limit")
	}
	if _, exists := m.commands[command.ID]; exists {
		return domain.Conflict("duplicate_command_id", "command id already exists")
	}

	m.commands[command.ID] = command
	return nil
}

func (m *MemoryRepository) Command(_ context.Context, id string) (domain.GateCommand, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	command, exists := m.commands[id]
	if !exists {
		return domain.GateCommand{}, domain.NotFound("command_not_found", "command id was not found")
	}
	return cloneCommand(command), nil
}

func (m *MemoryRepository) ReviewCommand(
	_ context.Context,
	commandID string,
	decision domain.InterlockDecision,
	token string,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.Review(decision, token, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := gate
	if decision.IsClear() {
		if err := nextGate.Reserve(nextCommand.ID, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), nextGate, nil
}

func (m *MemoryRepository) ExecuteCommand(
	_ context.Context,
	commandID string,
	token string,
	operator string,
	simulateFault bool,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	command, exists := m.commands[commandID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "command id was not found")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.BeginExecution(token, operator, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := gate
	if err := nextGate.StartMovement(nextCommand.ID, now); err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
	}

	if simulateFault {
		if err := nextGate.Settle(nextCommand.TargetAperture, true, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextCommand.Abort("simulated_gate_fault", now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	} else {
		if err := nextGate.Settle(nextCommand.TargetAperture, false, now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
		if err := nextCommand.Complete(now); err != nil {
			return domain.GateCommand{}, domain.Gate{}, err
		}
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	return cloneCommand(nextCommand), nextGate, nil
}

func (m *MemoryRepository) CreatePlan(_ context.Context, plan domain.OperationPlan) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if _, exists := m.plans[plan.ID]; exists {
		return domain.Conflict("duplicate_plan_id", "plan id already exists")
	}
	for _, stage := range plan.Stages {
		gate, exists := m.gates[stage.GateID]
		if !exists {
			return domain.NotFound("gate_not_found", "stage gate id was not found")
		}
		if stage.TargetAperture > gate.MaxAperture {
			return domain.Invalid("invalid_target_aperture", "stage target aperture is outside the gate limit")
		}
	}

	m.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (m *MemoryRepository) Plan(_ context.Context, id string) (domain.PlanProjection, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plan, exists := m.plans[id]
	if !exists {
		return domain.PlanProjection{}, domain.NotFound("plan_not_found", "plan id was not found")
	}
	return m.projectionLocked(plan), nil
}

func (m *MemoryRepository) AdvancePlan(
	_ context.Context,
	planID string,
	commandID string,
	requestedBy string,
	now time.Time,
) (domain.PlanProjection, domain.GateCommand, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan, exists := m.plans[planID]
	if !exists {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.NotFound("plan_not_found", "plan id was not found")
	}

	outcomes := make([]domain.StageStatus, len(plan.Stages))
	for i, stage := range plan.Stages {
		if stage.CommandID == "" {
			outcomes[i] = domain.StageWaiting
			continue
		}
		command, exists := m.commands[stage.CommandID]
		if !exists {
			return domain.PlanProjection{}, domain.GateCommand{}, domain.Internal(
				"plan_command_missing",
				"stage command record is missing",
			)
		}
		outcomes[i] = domain.StageOutcomeOf(command.Status)
	}

	switch domain.PlanStatusOf(outcomes) {
	case domain.PlanBlocked:
		stage := plan.Stages[firstFailedStage(outcomes)]
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Conflict(
			"plan_blocked",
			fmt.Sprintf("stage %d command %s ended without completion", stage.Sequence, stage.CommandID),
		)
	case domain.PlanCompleted:
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Conflict(
			"plan_already_completed",
			"all plan stages are already completed",
		)
	}

	for i, outcome := range outcomes {
		if outcome == domain.StageActive {
			return domain.PlanProjection{}, domain.GateCommand{}, domain.Conflict(
				"plan_stage_in_progress",
				fmt.Sprintf("stage %d is still in progress", plan.Stages[i].Sequence),
			)
		}
	}

	next := -1
	for i, outcome := range outcomes {
		if outcome == domain.StageWaiting {
			next = i
			break
		}
	}
	if next < 0 {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Internal(
			"plan_stage_missing",
			"no stage is available to start",
		)
	}

	stage := plan.Stages[next]
	gate, exists := m.gates[stage.GateID]
	if !exists {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.NotFound("gate_not_found", "stage gate id was not found")
	}
	if gate.Status == domain.GateFaulted {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Conflict("gate_faulted", "stage gate is faulted")
	}
	if gate.Aperture == stage.TargetAperture {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Conflict(
			"plan_condition_changed",
			"gate aperture already matches the stage target",
		)
	}

	command, err := domain.NewGateCommand(
		commandID,
		gate.ID,
		stage.TargetAperture,
		gate.Aperture,
		gate.MaxAperture,
		stage.Reason,
		requestedBy,
		now,
	)
	if err != nil {
		return domain.PlanProjection{}, domain.GateCommand{}, err
	}

	nextPlan := clonePlan(plan)
	if err := nextPlan.MarkStageStarted(next, command.ID, now); err != nil {
		return domain.PlanProjection{}, domain.GateCommand{}, err
	}

	m.commands[command.ID] = command
	m.plans[nextPlan.ID] = nextPlan
	return m.projectionLocked(nextPlan), cloneCommand(command), nil
}

func firstFailedStage(outcomes []domain.StageStatus) int {
	for i, outcome := range outcomes {
		if outcome == domain.StageFailed {
			return i
		}
	}
	return 0
}

func (m *MemoryRepository) projectionLocked(plan domain.OperationPlan) domain.PlanProjection {
	commands := make(map[string]domain.GateCommand)
	for _, stage := range plan.Stages {
		if stage.CommandID == "" {
			continue
		}
		if command, exists := m.commands[stage.CommandID]; exists {
			commands[command.ID] = cloneCommand(command)
		}
	}
	return domain.PlanProjection{Plan: clonePlan(plan), Commands: commands}
}

func clonePlan(plan domain.OperationPlan) domain.OperationPlan {
	stages := make([]domain.PlanStage, len(plan.Stages))
	copy(stages, plan.Stages)
	plan.Stages = stages
	return plan
}

func cloneCommand(command domain.GateCommand) domain.GateCommand {
	if command.Interlock == nil {
		return command
	}
	decision := *command.Interlock
	command.Interlock = &decision
	return command
}
