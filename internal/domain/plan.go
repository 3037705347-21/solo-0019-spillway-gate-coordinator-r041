package domain

import (
	"strings"
	"time"
)

type StageStatus string

const (
	StageWaiting   StageStatus = "waiting"
	StageActive    StageStatus = "active"
	StageCompleted StageStatus = "completed"
	StageFailed    StageStatus = "failed"
)

type PlanStatus string

const (
	PlanActive    PlanStatus = "active"
	PlanBlocked   PlanStatus = "blocked"
	PlanCompleted PlanStatus = "completed"
)

const maxPlanStages = 32

type PlanStage struct {
	Sequence       int
	GateID         string
	TargetAperture float64
	Reason         string
	CommandID      string
}

type OperationPlan struct {
	ID          string
	Name        string
	RequestedBy string
	Stages      []PlanStage
	CreatedAt   time.Time
	UpdatedAt   time.Time
	Version     int64
}

type PlanStageSpec struct {
	GateID         string
	TargetAperture float64
	Reason         string
}

func NewOperationPlan(
	planID string,
	name string,
	requestedBy string,
	specs []PlanStageSpec,
	now time.Time,
) (OperationPlan, error) {
	planID = strings.TrimSpace(planID)
	if planID == "" {
		return OperationPlan{}, Invalid("invalid_plan_id", "plan id is required")
	}

	name = strings.TrimSpace(name)
	if name == "" {
		return OperationPlan{}, Invalid("invalid_plan_name", "plan name is required")
	}

	requestedBy = strings.TrimSpace(requestedBy)
	if requestedBy == "" {
		return OperationPlan{}, Invalid("invalid_requested_by", "requested_by is required")
	}

	if len(specs) == 0 || len(specs) > maxPlanStages {
		return OperationPlan{}, Invalid("invalid_plan_stages", "plan must define between 1 and 32 stages")
	}

	stages := make([]PlanStage, len(specs))
	for i, spec := range specs {
		gateID := strings.TrimSpace(spec.GateID)
		if gateID == "" {
			return OperationPlan{}, Invalid("invalid_gate_reference", "stage gate id is required")
		}
		reason := strings.TrimSpace(spec.Reason)
		if reason == "" {
			return OperationPlan{}, Invalid("invalid_stage_reason", "stage reason is required")
		}
		if spec.TargetAperture < 0 {
			return OperationPlan{}, Invalid("invalid_target_aperture", "stage target aperture must be zero or greater")
		}
		stages[i] = PlanStage{
			Sequence:       i + 1,
			GateID:         gateID,
			TargetAperture: spec.TargetAperture,
			Reason:         reason,
		}
	}

	return OperationPlan{
		ID:          planID,
		Name:        name,
		RequestedBy: requestedBy,
		Stages:      stages,
		CreatedAt:   now.UTC(),
		UpdatedAt:   now.UTC(),
		Version:     1,
	}, nil
}

func (p *OperationPlan) MarkStageStarted(index int, commandID string, now time.Time) error {
	if index < 0 || index >= len(p.Stages) {
		return Internal("plan_stage_index_invalid", "stage index is outside the plan")
	}
	if p.Stages[index].CommandID != "" {
		return Conflict("plan_stage_already_started", "stage already has a command")
	}

	commandID = strings.TrimSpace(commandID)
	if commandID == "" {
		return Invalid("invalid_command_id", "command id is required")
	}

	p.Stages[index].CommandID = commandID
	p.UpdatedAt = now.UTC()
	p.Version++
	return nil
}

// StageOutcomeOf folds a command status into the stage outcome it proves;
// terminal command states are one-way, so a completed stage stays completed.
func StageOutcomeOf(status CommandStatus) StageStatus {
	switch status {
	case CommandCompleted:
		return StageCompleted
	case CommandRejected, CommandAborted:
		return StageFailed
	default:
		return StageActive
	}
}

func PlanStatusOf(outcomes []StageStatus) PlanStatus {
	completed := 0
	for _, outcome := range outcomes {
		if outcome == StageFailed {
			return PlanBlocked
		}
		if outcome == StageCompleted {
			completed++
		}
	}
	if completed == len(outcomes) {
		return PlanCompleted
	}
	return PlanActive
}

// PlanProjection pairs a plan with the command records its stages started,
// so stage and plan status are always derived from real command facts.
type PlanProjection struct {
	Plan     OperationPlan
	Commands map[string]GateCommand
}

func (projection PlanProjection) StageOutcome(stage PlanStage) StageStatus {
	if stage.CommandID == "" {
		return StageWaiting
	}
	command, exists := projection.Commands[stage.CommandID]
	if !exists {
		return StageWaiting
	}
	return StageOutcomeOf(command.Status)
}

func (projection PlanProjection) Outcomes() []StageStatus {
	outcomes := make([]StageStatus, len(projection.Plan.Stages))
	for i, stage := range projection.Plan.Stages {
		outcomes[i] = projection.StageOutcome(stage)
	}
	return outcomes
}

func (projection PlanProjection) Status() PlanStatus {
	return PlanStatusOf(projection.Outcomes())
}
