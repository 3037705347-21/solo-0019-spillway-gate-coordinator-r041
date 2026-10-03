package httpapi

import (
	"net/http"
	"strconv"

	"example.com/spillway-gate-coordinator/internal/contracts"
)

func (h *handler) createPlan(w http.ResponseWriter, r *http.Request) {
	var request contracts.CreatePlanRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDomainError(w, err)
		return
	}

	plan, err := h.service.CreatePlan(r.Context(), request)
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusCreated, contracts.CreatePlanResponse{Plan: planView(plan)})
}

func (h *handler) plan(w http.ResponseWriter, r *http.Request) {
	plan, err := h.service.Plan(r.Context(), r.PathValue("id"))
	if err != nil {
		writeDomainError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, contracts.PlanResponse{Plan: planView(plan)})
}

func (h *handler) advancePlan(w http.ResponseWriter, r *http.Request) {
	sequence, err := strconv.Atoi(r.PathValue("sequence"))
	if err != nil {
		writeDomainError(w, invalidStageSequence())
		return
	}

	var request contracts.AdvancePlanRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDomainError(w, err)
		return
	}

	plan, command, gate, err := h.service.AdvancePlan(
		r.Context(),
		r.PathValue("id"),
		sequence,
		request,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	stage, stageErr := plan.Stage(sequence)
	if stageErr != nil {
		writeDomainError(w, stageErr)
		return
	}
	writeJSON(w, http.StatusOK, contracts.AdvancePlanResponse{
		Plan:    planView(plan),
		Stage:   stageView(*stage),
		Command: commandView(command, true),
		Gate:    gateView(gate),
	})
}

func (h *handler) executePlanStage(w http.ResponseWriter, r *http.Request) {
	sequence, err := strconv.Atoi(r.PathValue("sequence"))
	if err != nil {
		writeDomainError(w, invalidStageSequence())
		return
	}

	var request contracts.ExecutePlanStageRequest
	if err := decodeJSON(w, r, &request); err != nil {
		writeDomainError(w, err)
		return
	}

	plan, command, gate, err := h.service.ExecutePlanStage(
		r.Context(),
		r.PathValue("id"),
		sequence,
		request,
	)
	if err != nil {
		writeDomainError(w, err)
		return
	}

	stage, stageErr := plan.Stage(sequence)
	if stageErr != nil {
		writeDomainError(w, stageErr)
		return
	}
	writeJSON(w, http.StatusOK, contracts.ExecutePlanStageResponse{
		Plan:    planView(plan),
		Stage:   stageView(*stage),
		Command: commandView(command, false),
		Gate:    gateView(gate),
	})
}
