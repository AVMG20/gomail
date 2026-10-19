package ai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
)

// Question types of the Decisions API.
const (
	QuestionChoice = "choice" // pick one option; criteria: option -> guidance
	QuestionNoul   = "noul"   // yes/no probability; criteria keys "true" and "false"
	QuestionScore  = "score"  // ordinal; criteria: ordered array of level descriptions
)

// Question is one decision question. Criteria is a map[string]string for
// choice/noul and a []string for score.
type Question struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
	Criteria     any    `json:"criteria"`
}

// DecisionRequest is a POST /alpha/decisions body.
type DecisionRequest struct {
	Model     string              `json:"model"`
	State     any                 `json:"state"`
	Questions map[string]Question `json:"questions"`
	SessionID string              `json:"session_id,omitempty"`
	User      string              `json:"user,omitempty"`
	Provider  map[string]any      `json:"provider,omitempty"`
	Trace     map[string]any      `json:"trace,omitempty"`
}

// Answer is the answer to one question.
type Answer struct {
	Type          string             `json:"type"`
	Noul          *float64           `json:"noul,omitempty"`
	Choice        string             `json:"choice,omitempty"`
	Score         *float64           `json:"score,omitempty"`
	Confidence    float64            `json:"confidence,omitempty"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
	Legend        json.RawMessage    `json:"legend,omitempty"`
}

// DecisionResponse is the API response.
type DecisionResponse struct {
	ID       string            `json:"id"`
	Model    string            `json:"model"`
	Provider string            `json:"provider"`
	Answers  map[string]Answer `json:"answers"`
	Usage    struct {
		InputTokens  int     `json:"input_tokens"`
		OutputTokens int     `json:"output_tokens"`
		Cost         float64 `json:"cost"`
	} `json:"usage"`
}

// Decide calls the Decisions API.
func (c *Client) Decide(ctx context.Context, req DecisionRequest) (*DecisionResponse, error) {
	if len(req.Questions) == 0 {
		return nil, errors.New("no questions to ask")
	}
	if len(req.SessionID) > 256 {
		req.SessionID = req.SessionID[:256]
	}
	api, err := c.api(true)
	if err != nil {
		return nil, err
	}
	var resp DecisionResponse
	if err := api.JSON(ctx, http.MethodPost, "alpha/decisions", req, &resp); err != nil {
		return nil, friendly(err)
	}
	c.report(Usage{Kind: "decision", Model: req.Model, Cost: resp.Usage.Cost, InputTokens: resp.Usage.InputTokens, OutputTokens: resp.Usage.OutputTokens})
	for name := range req.Questions {
		if _, ok := resp.Answers[name]; !ok {
			return nil, fmt.Errorf("the classifier did not answer %q", name)
		}
	}
	return &resp, nil
}

// YesProbability extracts P(yes) from a noul answer (or a choice answer's
// "true" probability), clamped to [0, 1].
func (a Answer) YesProbability() (float64, bool) {
	var p float64
	switch {
	case a.Noul != nil:
		p = *a.Noul
	case a.Probabilities != nil:
		v, ok := a.Probabilities["true"]
		if !ok {
			return 0, false
		}
		p = v
	case a.Choice == "true":
		p = a.Confidence
	case a.Choice == "false":
		p = 1 - a.Confidence
	default:
		return 0, false
	}
	return min(1, max(0, p)), true
}
