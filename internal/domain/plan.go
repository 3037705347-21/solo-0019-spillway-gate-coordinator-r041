package domain

import (
	"strconv"
	"strings"
	"time"
)

type PlanStatus string

const (
	PlanActive    PlanStatus = "active"
	PlanCompleted PlanStatus = "completed"
	PlanHalted    PlanStatus = "halted"
)

type StageStatus string

const (
	StageWaiting   StageStatus = "waiting"
	StageArmed     StageStatus = "armed"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
)

const maxPlanStages = 64

// StageDraft is the intent captured when a plan is registered. No command is
// created and no gate is touched until the stage is actually started.
type StageDraft struct {
	GateID         string
	TargetAperture float64
	Reason         string
}

// PlanStage is one ordered step of a coordinated plan. While waiting it only
// describes intent; once the stage is started it is bound to the real command
// that owns the gate.
type PlanStage struct {
	Sequence       int
	GateID         string
	TargetAperture float64
	Reason         string
	Status         StageStatus
	CommandID      string
	StartedAt      time.Time
	FinishedAt     time.Time
}

type OperationPlan struct {
	ID          string
	Name        string
	RequestedBy string
	Status      PlanStatus
	Stages      []PlanStage
	HaltReason  string
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

func NewOperationPlan(
	id string,
	name string,
	requestedBy string,
	drafts []StageDraft,
	now time.Time,
) (OperationPlan, error) {
	id = strings.TrimSpace(id)
	if !gateIDPattern.MatchString(id) {
		return OperationPlan{}, Invalid("invalid_plan_id", "plan id must use lowercase letters, digits, and hyphens")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return OperationPlan{}, Invalid("invalid_plan_name", "plan name is required")
	}

	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		return OperationPlan{}, Invalid("invalid_plan_requester", "requested_by is required")
	}

	if len(drafts) == 0 {
		return OperationPlan{}, Invalid("invalid_plan_stages", "at least one plan stage is required")
	}
	if len(drafts) > maxPlanStages {
		return OperationPlan{}, Invalid("invalid_plan_stages", "a plan cannot contain more than "+strconv.Itoa(maxPlanStages)+" stages")
	}

	stages := make([]PlanStage, 0, len(drafts))
	for index, draft := range drafts {
		gateID := strings.TrimSpace(draft.GateID)
		if !gateIDPattern.MatchString(gateID) {
			return OperationPlan{}, Invalid("invalid_stage_gate", "each stage must reference a valid gate id")
		}
		reason := strings.TrimSpace(draft.Reason)
		if reason == "" {
			return OperationPlan{}, Invalid("invalid_stage_reason", "each stage requires a reason")
		}
		if draft.TargetAperture < 0 {
			return OperationPlan{}, Invalid("invalid_stage_target_aperture", "stage target aperture cannot be negative")
		}

		stages = append(stages, PlanStage{
			Sequence:       index + 1,
			GateID:         gateID,
			TargetAperture: draft.TargetAperture,
			Reason:         reason,
			Status:         StageWaiting,
		})
	}

	return OperationPlan{
		ID:          id,
		Name:        name,
		RequestedBy: requestedBy,
		Status:      PlanActive,
		Stages:      stages,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
		Version:     1,
	}, nil
}

// Stage returns the stage with the 1-based sequence number.
func (p *OperationPlan) Stage(sequence int) (*PlanStage, error) {
	if sequence < 1 || sequence > len(p.Stages) {
		return nil, NotFound("stage_not_found", "plan stage was not found")
	}
	return &p.Stages[sequence-1], nil
}

// ReadyToArm enforces plan-level and ordering rules for starting a stage. It
// deliberately does not look at gates or commands: the repository re-confirms
// the predecessor completion fact and the current gate conditions under its
// lock.
func (p *OperationPlan) ReadyToArm(sequence int) (*PlanStage, error) {
	switch p.Status {
	case PlanHalted:
		return nil, Conflict("plan_halted", "plan is halted after a failed stage and cannot advance")
	case PlanCompleted:
		return nil, Conflict("plan_already_completed", "completed plans cannot advance")
	}

	if sequence < 1 || sequence > len(p.Stages) {
		return nil, Invalid("invalid_stage_sequence", "stage sequence is outside the plan")
	}

	for index := range p.Stages {
		if p.Stages[index].Sequence >= sequence {
			break
		}
		if p.Stages[index].Status != StageCompleted && p.Stages[index].Status != StageFailed {
			return nil, Conflict(
				"predecessor_not_completed",
				"a later stage cannot start before the previous stage has completed",
			)
		}
	}

	stage := &p.Stages[sequence-1]
	switch stage.Status {
	case StageArmed:
		return nil, Conflict("stage_already_armed", "stage has already been started and does not need to advance again")
	case StageCompleted:
		return nil, Conflict("stage_already_completed", "completed stages cannot be advanced again")
	case StageFailed:
		return nil, Conflict("stage_failed", "failed stages cannot be advanced again")
	}
	return stage, nil
}

// StageForExecution returns the stage bound to a command the operator wants to
// execute. Only an armed stage of an active plan can be executed.
func (p *OperationPlan) StageForExecution(sequence int) (*PlanStage, error) {
	switch p.Status {
	case PlanHalted:
		return nil, Conflict("plan_halted", "plan is halted after a failed stage")
	case PlanCompleted:
		return nil, Conflict("plan_already_completed", "completed plans cannot execute more stages")
	}

	stage, err := p.Stage(sequence)
	if err != nil {
		return nil, err
	}
	if stage.Status != StageArmed {
		return nil, Conflict("stage_not_armed", "stage must be started before it can be executed")
	}
	if strings.TrimSpace(stage.CommandID) == "" {
		return nil, Internal("stage_command_missing", "armed stage is not bound to a command")
	}
	return stage, nil
}

// Arm binds a waiting stage to the command that just reserved its gate.
func (s *PlanStage) Arm(commandID string, now time.Time) error {
	if s.Status != StageWaiting {
		return Conflict("stage_not_waiting", "only waiting stages can be started")
	}
	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return Invalid("invalid_stage_command", "a started stage must reference a command")
	}

	s.CommandID = commandID
	s.Status = StageArmed
	s.StartedAt = now.UTC()
	return nil
}

// SettleStage records the real execution outcome of an armed stage. Earlier
// stages are never touched: a failure on a later stage only fails that stage
// and halts the plan, leaving completed history intact.
func (p *OperationPlan) SettleStage(sequence int, completed bool, terminalReason string, now time.Time) error {
	stage, err := p.Stage(sequence)
	if err != nil {
		return err
	}
	if stage.Status != StageArmed {
		return Conflict("stage_not_armed", "only armed stages can settle")
	}

	stage.FinishedAt = now.UTC()
	if completed {
		stage.Status = StageCompleted
	} else {
		stage.Status = StageFailed
	}

	p.UpdatedAt = now.UTC()
	p.Version++

	if !completed {
		reason := strings.TrimSpace(terminalReason)
		if reason == "" {
			reason = "stage_execution_failed"
		}
		p.Status = PlanHalted
		p.HaltReason = reason
		return nil
	}

	for index := range p.Stages {
		if p.Stages[index].Status != StageCompleted {
			return nil
		}
	}
	p.Status = PlanCompleted
	return nil
}
