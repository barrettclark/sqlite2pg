package wizard

import (
	"encoding/json"
	"net/http"
)

// DecisionRequest is the body of POST /api/columns/{table}/{column}/decision.
type DecisionRequest struct {
	TargetType string `json:"target_type"`
	Rationale  string `json:"rationale"`
}

// NewMux wires the wizard's REST API (and, once added, its static
// frontend) against st.
func NewMux(st *State) *http.ServeMux {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /api/summary", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusOK, st.Summary())
	})

	mux.HandleFunc("POST /api/columns/{table}/{column}/decision", func(w http.ResponseWriter, r *http.Request) {
		var req DecisionRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if err := st.ApplyDecision(r.PathValue("table"), r.PathValue("column"), req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		writeJSON(w, http.StatusOK, st.Summary())
	})

	mux.HandleFunc("POST /api/finish", func(w http.ResponseWriter, r *http.Request) {
		if err := st.Finish(); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		writeJSON(w, http.StatusOK, map[string]bool{"done": true})
	})

	registerStaticRoutes(mux)

	return mux
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(v)
}
