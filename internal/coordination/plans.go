package coordination

import (
	"context"
	"strings"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func (s *Service) CreatePlan(
	ctx context.Context,
	request contracts.CreatePlanRequest,
) (domain.PlanProjection, error) {
	planID, err := s.newID("plan")
	if err != nil {
		return domain.PlanProjection{}, domain.Internal("plan_id_generation_failed", "plan id could not be generated")
	}

	specs := make([]domain.PlanStageSpec, len(request.Stages))
	for i, stage := range request.Stages {
		specs[i] = domain.PlanStageSpec{
			GateID:         stage.GateID,
			TargetAperture: stage.TargetAperture,
			Reason:         stage.Reason,
		}
	}

	plan, err := domain.NewOperationPlan(planID, request.Name, request.RequestedBy, specs, s.now())
	if err != nil {
		return domain.PlanProjection{}, err
	}
	if err := s.repository.CreatePlan(ctx, plan); err != nil {
		return domain.PlanProjection{}, err
	}
	return domain.PlanProjection{Plan: plan, Commands: map[string]domain.GateCommand{}}, nil
}

func (s *Service) Plan(ctx context.Context, id string) (domain.PlanProjection, error) {
	return s.repository.Plan(ctx, id)
}

func (s *Service) AdvancePlan(
	ctx context.Context,
	planID string,
	request contracts.AdvancePlanRequest,
) (domain.PlanProjection, domain.GateCommand, error) {
	requestedBy := strings.TrimSpace(request.RequestedBy)
	if requestedBy == "" {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Invalid(
			"invalid_requested_by",
			"requested_by is required",
		)
	}

	commandID, err := s.newID("cmd")
	if err != nil {
		return domain.PlanProjection{}, domain.GateCommand{}, domain.Internal(
			"command_id_generation_failed",
			"command id could not be generated",
		)
	}
	return s.repository.AdvancePlan(ctx, planID, commandID, requestedBy, s.now())
}
