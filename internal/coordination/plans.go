package coordination

import (
	"context"

	"example.com/spillway-gate-coordinator/internal/contracts"
	"example.com/spillway-gate-coordinator/internal/domain"
)

func (s *Service) CreatePlan(
	ctx context.Context,
	request contracts.CreatePlanRequest,
) (domain.OperationPlan, error) {
	drafts := make([]domain.StageDraft, 0, len(request.Stages))
	for _, stage := range request.Stages {
		drafts = append(drafts, domain.StageDraft{
			GateID:         stage.GateID,
			TargetAperture: stage.TargetAperture,
			Reason:         stage.Reason,
		})
	}

	plan, err := domain.NewOperationPlan(
		request.ID,
		request.Name,
		request.RequestedBy,
		drafts,
		s.now(),
	)
	if err != nil {
		return domain.OperationPlan{}, err
	}
	if err := s.repository.CreatePlan(ctx, plan); err != nil {
		return domain.OperationPlan{}, err
	}
	return plan, nil
}

func (s *Service) Plan(ctx context.Context, id string) (domain.OperationPlan, error) {
	return s.repository.Plan(ctx, id)
}

// AdvancePlan starts a single stage. The clear interlock decision and the
// execution token are produced here but the command creation, gate
// reservation, and stage binding only commit together inside the repository.
func (s *Service) AdvancePlan(
	ctx context.Context,
	planID string,
	sequence int,
	request contracts.AdvancePlanRequest,
) (domain.OperationPlan, domain.GateCommand, domain.Gate, error) {
	decision, err := domain.NewInterlockDecision(
		"clear",
		request.ConfirmedBy,
		request.Note,
		s.now(),
	)
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, err
	}

	commandID, err := s.newID("cmd")
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Internal(
			"command_id_generation_failed",
			"command id could not be generated",
		)
	}
	token, err := s.newToken()
	if err != nil {
		return domain.OperationPlan{}, domain.GateCommand{}, domain.Gate{}, domain.Internal(
			"execution_token_generation_failed",
			"execution token could not be generated",
		)
	}

	return s.repository.AdvancePlan(
		ctx,
		planID,
		sequence,
		commandID,
		decision,
		token,
		s.now(),
	)
}

func (s *Service) ExecutePlanStage(
	ctx context.Context,
	planID string,
	sequence int,
	request contracts.ExecutePlanStageRequest,
) (domain.OperationPlan, domain.GateCommand, domain.Gate, error) {
	return s.repository.ExecutePlanStage(
		ctx,
		planID,
		sequence,
		request.Token,
		request.Operator,
		request.SimulateFault,
		s.now(),
	)
}
