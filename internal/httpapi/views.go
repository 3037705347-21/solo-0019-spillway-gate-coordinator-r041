package httpapi

import (
	"time"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func formatOptionalTime(value time.Time) string {
	if value.IsZero() {
		return ""
	}
	return formatTime(value)
}

func gateView(gate domain.Gate) contracts.GateView {
	return contracts.GateView{
		ID:              gate.ID,
		Name:            gate.Name,
		Status:          string(gate.Status),
		Aperture:        gate.Aperture,
		MaxAperture:     gate.MaxAperture,
		ActiveCommandID: gate.ActiveCommandID,
		UpdatedAt:       formatTime(gate.UpdatedAt),
		Version:         gate.Version,
	}
}

func commandView(command domain.GateCommand, includeToken bool) contracts.CommandView {
	view := contracts.CommandView{
		ID:               command.ID,
		GateID:           command.GateID,
		TargetAperture:   command.TargetAperture,
		StartingAperture: command.StartingAperture,
		Reason:           command.Reason,
		RequestedBy:      command.RequestedBy,
		Status:           string(command.Status),
		ExecutedBy:       command.ExecutedBy,
		TerminalReason:   command.TerminalReason,
		PlanID:           command.PlanID,
		PlanStage:        command.PlanStage,
		CreatedAt:        formatTime(command.CreatedAt),
		UpdatedAt:        formatTime(command.UpdatedAt),
		Version:          command.Version,
	}

	if includeToken && command.Status == domain.CommandApproved {
		view.ExecutionToken = command.ExecutionToken
	}
	if command.Interlock != nil {
		view.Interlock = &contracts.InterlockDecisionView{
			Verdict:    string(command.Interlock.Verdict),
			Verifier:   command.Interlock.Verifier,
			Note:       command.Interlock.Note,
			ReviewedAt: formatTime(command.Interlock.ReviewedAt),
		}
	}
	return view
}

func stageView(stage domain.PlanStage) contracts.PlanStageView {
	return contracts.PlanStageView{
		Sequence:       stage.Sequence,
		GateID:         stage.GateID,
		TargetAperture: stage.TargetAperture,
		Reason:         stage.Reason,
		Status:         string(stage.Status),
		CommandID:      stage.CommandID,
		StartedAt:      formatOptionalTime(stage.StartedAt),
		FinishedAt:     formatOptionalTime(stage.FinishedAt),
	}
}

func planView(plan domain.OperationPlan) contracts.PlanView {
	stages := make([]contracts.PlanStageView, 0, len(plan.Stages))
	for _, stage := range plan.Stages {
		stages = append(stages, stageView(stage))
	}
	return contracts.PlanView{
		ID:          plan.ID,
		Name:        plan.Name,
		RequestedBy: plan.RequestedBy,
		Status:      string(plan.Status),
		HaltReason:  plan.HaltReason,
		Stages:      stages,
		CreatedAt:   formatTime(plan.CreatedAt),
		UpdatedAt:   formatTime(plan.UpdatedAt),
		Version:     plan.Version,
	}
}
