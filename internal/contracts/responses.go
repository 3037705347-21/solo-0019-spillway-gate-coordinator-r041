package contracts

type ErrorResponse struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type InterlockDecisionView struct {
	Verdict    string `json:"verdict"`
	Verifier   string `json:"verifier"`
	Note       string `json:"note"`
	ReviewedAt string `json:"reviewed_at"`
}

type GateView struct {
	ID              string  `json:"id"`
	Name            string  `json:"name"`
	Status          string  `json:"status"`
	Aperture        float64 `json:"aperture"`
	MaxAperture     float64 `json:"max_aperture"`
	ActiveCommandID string  `json:"active_command_id,omitempty"`
	UpdatedAt       string  `json:"updated_at"`
	Version         int64   `json:"version"`
}

type CommandView struct {
	ID               string                 `json:"id"`
	GateID           string                 `json:"gate_id"`
	TargetAperture   float64                `json:"target_aperture"`
	StartingAperture float64                `json:"starting_aperture"`
	Reason           string                 `json:"reason"`
	RequestedBy      string                 `json:"requested_by"`
	Status           string                 `json:"status"`
	Interlock        *InterlockDecisionView `json:"interlock,omitempty"`
	ExecutionToken   string                 `json:"execution_token,omitempty"`
	ExecutedBy       string                 `json:"executed_by,omitempty"`
	TerminalReason   string                 `json:"terminal_reason,omitempty"`
	PlanID           string                 `json:"plan_id,omitempty"`
	PlanStage        int                    `json:"plan_stage,omitempty"`
	CreatedAt        string                 `json:"created_at"`
	UpdatedAt        string                 `json:"updated_at"`
	Version          int64                  `json:"version"`
}

type PlanStageView struct {
	Sequence       int     `json:"sequence"`
	GateID         string  `json:"gate_id"`
	TargetAperture float64 `json:"target_aperture"`
	Reason         string  `json:"reason"`
	Status         string  `json:"status"`
	CommandID      string  `json:"command_id,omitempty"`
	StartedAt      string  `json:"started_at,omitempty"`
	FinishedAt     string  `json:"finished_at,omitempty"`
}

type PlanView struct {
	ID          string          `json:"id"`
	Name        string          `json:"name"`
	RequestedBy string          `json:"requested_by"`
	Status      string          `json:"status"`
	HaltReason  string          `json:"halt_reason,omitempty"`
	Stages      []PlanStageView `json:"stages"`
	CreatedAt   string          `json:"created_at"`
	UpdatedAt   string          `json:"updated_at"`
	Version     int64           `json:"version"`
}

type RegisterGateResponse struct {
	Gate GateView `json:"gate"`
}

type GateResponse struct {
	Gate GateView `json:"gate"`
}

type CommandResponse struct {
	Command CommandView `json:"command"`
}

type ReviewCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type ExecuteCommandResponse struct {
	Command CommandView `json:"command"`
	Gate    GateView    `json:"gate"`
}

type CreatePlanResponse struct {
	Plan PlanView `json:"plan"`
}

type PlanResponse struct {
	Plan PlanView `json:"plan"`
}

type AdvancePlanResponse struct {
	Plan    PlanView      `json:"plan"`
	Stage   PlanStageView `json:"stage"`
	Command CommandView   `json:"command"`
	Gate    GateView      `json:"gate"`
}

type ExecutePlanStageResponse struct {
	Plan    PlanView      `json:"plan"`
	Stage   PlanStageView `json:"stage"`
	Command CommandView   `json:"command"`
	Gate    GateView      `json:"gate"`
}
