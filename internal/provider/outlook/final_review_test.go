package outlook

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sync/atomic"
	"testing"

	"github.com/AVMG20/gomail/internal/provider"
)

// When a later $batch call fails outright (e.g. the action's deadline), the
// sub-requests of earlier calls were applied: the error must name only the
// ids that weren't, so the caller keeps the applied ones' local state.
func TestBatchCallFailureIsPartial(t *testing.T) {
	var calls atomic.Int64
	ok := batchHandler(func(string) int { return http.StatusOK })
	c := auditClient(t, func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) > 1 {
			w.WriteHeader(http.StatusBadRequest)
			json.NewEncoder(w).Encode(map[string]any{"error": map[string]string{"code": "x", "message": "bad"}})
			return
		}
		ok(w, r)
	})
	ids := make([]string, 25) // two $batch calls of 20 + 5
	for i := range ids {
		ids[i] = string(rune('a'+i)) + "id"
	}
	err := c.SetRead(context.Background(), ids, true)
	var pe *provider.PartialError
	if !errors.As(err, &pe) {
		t.Fatalf("err = %v, want a PartialError", err)
	}
	if len(pe.Failed) != 5 || pe.Failed[0] != ids[20] {
		t.Fatalf("failed = %v, want the last 5 ids", pe.Failed)
	}
}
