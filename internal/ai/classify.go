package ai

import (
	"context"
	"fmt"
	"math"
	"regexp"
	"strings"
	"time"

	"github.com/AVMG20/gomail/internal/models"
)

// DefaultClassifyInstructions is the per-label question template; {label}
// is replaced by the label name.
const DefaultClassifyInstructions = `You are sorting a user's email. Does the label "{label}" apply to this email? Answer true only if it clearly applies.`

// MaxLabelsPerRequest bounds the questions per Decisions request.
const MaxLabelsPerRequest = 20

// MaxBodyChars is how much body text is sent per email.
const MaxBodyChars = 4000

// Email is the classifier input.
type Email struct {
	From           models.Address
	To             []models.Address
	Subject        string
	Date           time.Time
	HasAttachments bool
	Body           string // plain text (or snippet)
}

func fmtAddr(a models.Address) string {
	if a.Name != "" && a.Email != "" {
		return a.Name + " <" + a.Email + ">"
	}
	return a.Name + a.Email
}

var (
	quoteHeaderRe = regexp.MustCompile(`(?im)^(on .{4,200} wrote:|-{2,} ?original message ?-{2,}|from: .+\r?\nsent: .+|am .{4,200} schrieb .+:|le .{4,200} a écrit ?:)\s*$`)
	sigRe         = regexp.MustCompile(`(?m)^-- ?$`)
	spaceRe       = regexp.MustCompile(`[ \t\x{00a0}]+`)
	blankRe       = regexp.MustCompile(`\n{3,}`)
)

// CleanBody strips quoted replies and signatures (cheaply) and truncates.
func CleanBody(s string, maxChars int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if loc := quoteHeaderRe.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	if loc := sigRe.FindStringIndex(s); loc != nil && loc[0] > 0 {
		s = s[:loc[0]]
	}
	var lines []string
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(strings.TrimSpace(l), ">") {
			continue
		}
		lines = append(lines, strings.TrimSpace(spaceRe.ReplaceAllString(l, " ")))
	}
	s = strings.TrimSpace(blankRe.ReplaceAllString(strings.Join(lines, "\n"), "\n\n"))
	if r := []rune(s); len(r) > maxChars {
		s = string(r[:maxChars]) + "…"
	}
	return s
}

// State builds the Decisions "state" object for an email.
func (e Email) State() map[string]any {
	to := make([]string, 0, len(e.To))
	for _, a := range e.To {
		to = append(to, fmtAddr(a))
	}
	st := map[string]any{
		"from":            fmtAddr(e.From),
		"to":              strings.Join(to, ", "),
		"subject":         e.Subject,
		"has_attachments": e.HasAttachments,
		"body":            CleanBody(e.Body, MaxBodyChars),
	}
	if !e.Date.IsZero() {
		st["date"] = e.Date.UTC().Format(time.RFC3339)
	}
	return st
}

// LabelDef is a label offered to the classifier.
type LabelDef struct {
	ID          string
	Name        string
	Description string
}

var unsafeKey = regexp.MustCompile(`[^a-z0-9_]+`)

// questionKeys assigns safe, unique question names to labels.
func questionKeys(labels []LabelDef) map[string]string {
	keys := map[string]string{}
	used := map[string]bool{}
	for i, l := range labels {
		k := "label_" + strings.Trim(unsafeKey.ReplaceAllString(strings.ToLower(l.ID), "_"), "_")
		if k == "label_" {
			k = fmt.Sprintf("label_%d", i)
		}
		base := k
		for n := 2; used[k]; n++ {
			k = fmt.Sprintf("%s_%d", base, n)
		}
		used[k] = true
		keys[k] = l.ID
	}
	return keys
}

// Questions builds one noul question per label.
func Questions(labels []LabelDef, instructions string) (map[string]Question, map[string]string) {
	if strings.TrimSpace(instructions) == "" {
		instructions = DefaultClassifyInstructions
	}
	keys := questionKeys(labels)
	byID := map[string]LabelDef{}
	for _, l := range labels {
		byID[l.ID] = l
	}
	qs := map[string]Question{}
	for k, id := range keys {
		l := byID[id]
		instr := strings.ReplaceAll(instructions, "{label}", l.Name)
		if !strings.Contains(instructions, "{label}") {
			instr = instructions + "\nLabel: " + l.Name
		}
		yes := l.Description
		if yes == "" {
			yes = fmt.Sprintf("The email fits the label %q.", l.Name)
		}
		qs[k] = Question{Type: QuestionNoul, Instructions: instr, Criteria: map[string]string{"true": yes, "false": "This label does not apply."}}
	}
	return qs, keys
}

// LabelScores is the classifier output for one email.
type LabelScores struct {
	Scores map[string]float64 // label id -> P(yes)
	Cost   float64            // USD
}

// ScoreLabels asks the Decisions API, in chunks of MaxLabelsPerRequest
// labels, whether each label applies to the email.
func (c *Client) ScoreLabels(ctx context.Context, model, instructions string, labels []LabelDef, e Email) (LabelScores, error) {
	out := LabelScores{Scores: map[string]float64{}}
	if len(labels) == 0 {
		return out, nil
	}
	if model == "" {
		model = JevModel
	}
	if !IsDecisionModel(model) {
		return out, fmt.Errorf("%q is not a decision model; choose %s (or %s) as the classifier model in Settings", model, JevModel, JevLatestModel)
	}
	state := e.State()
	for start := 0; start < len(labels); start += MaxLabelsPerRequest {
		chunk := labels[start:min(len(labels), start+MaxLabelsPerRequest)]
		qs, keys := Questions(chunk, instructions)
		resp, err := c.Decide(ctx, DecisionRequest{Model: model, State: state, Questions: qs})
		if err != nil {
			return out, err
		}
		out.Cost += resp.Usage.Cost
		for k, id := range keys {
			p, ok := resp.Answers[k].YesProbability()
			if !ok || math.IsNaN(p) {
				continue
			}
			out.Scores[id] = p
		}
	}
	return out, nil
}

// EstimateTokens approximates input tokens for classifying one email with
// stateChars of state text against labels.
func EstimateTokens(stateChars int, labels []LabelDef, instructions string) (tokens, requests int) {
	if strings.TrimSpace(instructions) == "" {
		instructions = DefaultClassifyInstructions
	}
	if len(labels) == 0 {
		return 0, 0
	}
	requests = (len(labels) + MaxLabelsPerRequest - 1) / MaxLabelsPerRequest
	labelChars := 0
	for _, l := range labels {
		labelChars += len(instructions) + len(l.Name) + len(l.Description) + 60
	}
	const overheadChars = 120 // JSON keys of the state object
	tokens = (requests*(stateChars+overheadChars) + labelChars) / 4
	return tokens, requests
}
