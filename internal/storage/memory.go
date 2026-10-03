package storage

import (
	"context"
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
	if command.PlanID != "" {
		return domain.GateCommand{}, domain.Gate{}, domain.Conflict(
			"plan_bound_command",
			"plan-bound commands must be executed through their plan stage",
		)
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand, nextGate, err := applyExecution(command, gate, token, operator, simulateFault, now)
	if err != nil {
		return domain.GateCommand{}, domain.Gate{}, err
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
	// A plan only records intent, but every referenced gate must already exist
	// and be operable; the aperture limits are re-checked again at advance time.
	for _, stage := range plan.Stages {
		gate, exists := m.gates[stage.GateID]
		if !exists {
			return domain.NotFound("gate_not_found", "stage references a gate id that was not found")
		}
		if gate.Status == domain.GateFaulted {
			return domain.Conflict("gate_faulted", "a faulted gate cannot be scheduled in a plan")
		}
		if stage.TargetAperture > gate.MaxAperture {
			return domain.Invalid("invalid_stage_target_aperture", "stage target aperture is outside the gate limit")
		}
	}

	m.plans[plan.ID] = clonePlan(plan)
	return nil
}

func (m *MemoryRepository) Plan(_ context.Context, id string) (domain.OperationPlan, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	plan, exists := m.plans[id]
	if !exists {
		return domain.OperationPlan{}, domain.NotFound("plan_not_found", "plan id was not found")
	}
	return clonePlan(plan), nil
}

// AdvancePlan starts one plan stage atomically: it re-confirms the real
// completion fact of the previous stage, re-reads the current gate conditions,
// and only then creates the command, applies the clear interlock, and reserves
// the gate. Any failed check returns without writing command, gate, or plan.
func (m *MemoryRepository) AdvancePlan(
	_ context.Context,
	planID string,
	sequence int,
	commandID string,
	decision domain.InterlockDecision,
	token string,
	now time.Time,
) (domain.OperationPlan, domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan, exists := m.plans[planID]
	if !exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.NotFound("plan_not_found", "plan id was not found")
	}
	nextPlan := clonePlan(plan)

	stage, err := nextPlan.ReadyToArm(sequence)
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	// Trust only the real predecessor command record, not cached stage state.
	if sequence > 1 {
		predecessor := nextPlan.Stages[sequence-2]
		if predecessor.Status != domain.StageCompleted {
			return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict(
				"predecessor_not_completed",
				"the previous stage has not completed",
			)
		}
		prevCommand, exists := m.commands[predecessor.CommandID]
		if !exists || prevCommand.Status != domain.CommandCompleted {
			return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict(
				"predecessor_not_completed",
				"the previous stage command has no recorded completion",
			)
		}
	}

	// Re-confirm the gate under the current conditions at start time.
	gate, exists := m.gates[stage.GateID]
	if !exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}
	if gate.Status == domain.GateFaulted {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict("gate_faulted", "faulted gates cannot start a new stage")
	}
	if gate.Status != domain.GateIdle || gate.ActiveCommandID != "" {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict("gate_not_idle", "gate is not available for the new stage")
	}

	command, err := domain.NewGateCommand(
		commandID,
		stage.GateID,
		stage.TargetAperture,
		gate.Aperture,
		gate.MaxAperture,
		stage.Reason,
		nextPlan.RequestedBy,
		nextPlan.ID,
		stage.Sequence,
		now,
	)
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}
	if _, exists := m.commands[command.ID]; exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict("duplicate_command_id", "command id already exists")
	}

	nextCommand := cloneCommand(command)
	if err := nextCommand.Review(decision, token, now); err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	nextGate := gate
	if err := nextGate.Reserve(nextCommand.ID, now); err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	if err := stage.Arm(nextCommand.ID, now); err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}
	nextPlan.UpdatedAt = now.UTC()
	nextPlan.Version++

	// Commit all three facts together.
	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	m.plans[nextPlan.ID] = nextPlan
	return clonePlan(nextPlan), cloneCommand(nextCommand), nextGate, nil
}

// ExecutePlanStage runs the command bound to one armed stage and records the
// real outcome on the stage and plan in the same commit.
func (m *MemoryRepository) ExecutePlanStage(
	_ context.Context,
	planID string,
	sequence int,
	token string,
	operator string,
	simulateFault bool,
	now time.Time,
) (domain.OperationPlan, domain.GateCommand, domain.Gate, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	plan, exists := m.plans[planID]
	if !exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.NotFound("plan_not_found", "plan id was not found")
	}
	nextPlan := clonePlan(plan)

	stage, err := nextPlan.StageForExecution(sequence)
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	command, exists := m.commands[stage.CommandID]
	if !exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.NotFound("command_not_found", "bound command id was not found")
	}
	if command.PlanID != planID || command.PlanStage != sequence {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Conflict("plan_command_mismatch", "command is not bound to this plan stage")
	}
	gate, exists := m.gates[command.GateID]
	if !exists {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.NotFound("gate_not_found", "gate id was not found")
	}

	nextCommand, nextGate, err := applyExecution(command, gate, token, operator, simulateFault, now)
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	completed := nextCommand.Status == domain.CommandCompleted
	if err := nextPlan.SettleStage(sequence, completed, nextCommand.TerminalReason, now); err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	m.commands[nextCommand.ID] = nextCommand
	m.gates[nextGate.ID] = nextGate
	m.plans[nextPlan.ID] = nextPlan
	return clonePlan(nextPlan), cloneCommand(nextCommand), nextGate, nil
}

// applyExecution performs the shared command-and-gate execution transition on
// in-memory copies; the caller owns the lock and decides what to commit.
func applyExecution(
	command domain.GateCommand,
	gate domain.Gate,
	token string,
	operator string,
	simulateFault bool,
	now time.Time,
) (domain.GateCommand, domain.Gate, error) {
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

	return nextCommand, nextGate, nil
}

func cloneCommand(command domain.GateCommand) domain.GateCommand {
	if command.Interlock == nil {
		return command
	}
	decision := *command.Interlock
	command.Interlock = &decision
	return command
}

func clonePlan(plan domain.OperationPlan) domain.OperationPlan {
	plan.Stages = append([]domain.PlanStage(nil), plan.Stages...)
	return plan
}
