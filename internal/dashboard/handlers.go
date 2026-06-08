package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"path"
	"strings"

	"m-tunnel/internal/gateway"
	"m-tunnel/internal/store"
)

type clientResponse struct {
	ID          string   `json:"id"`
	Token       string   `json:"token,omitempty"`
	PublicPort  int      `json:"public_port"`
	AllowedIPs  []string `json:"allowed_ips"`
	DisplayName string   `json:"display_name"`
	CreatedAt   int64    `json:"created_at"`
	UpdatedAt   int64    `json:"updated_at"`
	Online      bool     `json:"online"`
	BytesIn     int64    `json:"bytes_in"`
	BytesOut    int64    `json:"bytes_out"`
	Streams     int      `json:"streams"`
}

func (s *Server) registerRoutes(mux *http.ServeMux) {
	mux.HandleFunc("/", s.handleIndex)
	mux.HandleFunc("/api/clients", s.handleClients)
	mux.HandleFunc("/api/clients/", s.handleClientByID)
}

func (s *Server) handleIndex(w http.ResponseWriter, r *http.Request) {
	if r.URL.Path != "/" {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = io.WriteString(w, dashboardHTML)
}

func (s *Server) handleClients(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		clients, err := s.store.ListClients()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		stats := s.gw.GetStats()
		resp := make([]clientResponse, 0, len(clients))
		for _, c := range clients {
			st := stats[c.ID]
			resp = append(resp, toClientResponse(c, st))
		}
		writeJSON(w, resp)
	case http.MethodPost:
		s.handleCreateClient(w, r)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleClientByID(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimPrefix(r.URL.Path, "/api/clients/")
	id = path.Clean("/" + id)
	id = strings.TrimPrefix(id, "/")
	if id == "" || id == "." || strings.Contains(id, "/") {
		http.Error(w, "bad client id", http.StatusBadRequest)
		return
	}

	switch {
	case r.Method == http.MethodPut:
		s.handleUpdateClient(w, r, id)
	case r.Method == http.MethodDelete:
		s.handleDeleteClient(w, r, id)
	case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/regenerate-token"):
		s.handleRegenerateToken(w, r, id)
	default:
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
	}
}

func (s *Server) handleCreateClient(w http.ResponseWriter, r *http.Request) {
	var req struct {
		ID          string   `json:"id"`
		DisplayName string   `json:"display_name"`
		AllowedIPs  []string `json:"allowed_ips"`
		PublicPort  int      `json:"public_port"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	if req.ID == "" {
		http.Error(w, "id is required", http.StatusBadRequest)
		return
	}
	token := generateToken()
	port := req.PublicPort
	if port == 0 {
		var err error
		port, err = s.store.AllocatePort(s.pcfg.PortRange.Start, s.pcfg.PortRange.End)
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
	} else if err := s.store.ReservePort(port); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	client := &store.Client{
		ID:          req.ID,
		Token:       token,
		PublicPort:  port,
		AllowedIPs:  req.AllowedIPs,
		DisplayName: req.DisplayName,
	}
	if err := s.store.CreateClient(client); err != nil {
		_ = s.store.FreePort(port)
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	if err := s.gw.AddClient(client); err != nil {
		_ = s.store.DeleteClient(client.ID)
		_ = s.store.FreePort(port)
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, map[string]any{"client": toClientResponse(*client, s.gw.GetStats()[client.ID])})
}

func (s *Server) handleUpdateClient(w http.ResponseWriter, r *http.Request, id string) {
	client, err := s.store.GetClient(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	var req struct {
		DisplayName string   `json:"display_name"`
		AllowedIPs  []string `json:"allowed_ips"`
	}
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid JSON", http.StatusBadRequest)
		return
	}
	client.DisplayName = req.DisplayName
	client.AllowedIPs = req.AllowedIPs
	if err := s.store.UpdateClient(client); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.gw.UpdateClient(client); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, toClientResponse(*client, s.gw.GetStats()[client.ID]))
}

func (s *Server) handleDeleteClient(w http.ResponseWriter, r *http.Request, id string) {
	client, err := s.store.GetClient(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	if err := s.gw.RemoveClient(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.store.FreePort(client.PublicPort); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.store.DeleteClient(id); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) handleRegenerateToken(w http.ResponseWriter, r *http.Request, id string) {
	client, err := s.store.GetClient(id)
	if err != nil {
		http.Error(w, err.Error(), http.StatusNotFound)
		return
	}
	client.Token = generateToken()
	if err := s.store.UpdateClient(client); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	if err := s.gw.UpdateClient(client); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}
	writeJSON(w, toClientResponse(*client, s.gw.GetStats()[client.ID]))
}

func toClientResponse(c store.Client, st gateway.ClientStats) clientResponse {
	return clientResponse{
		ID:          c.ID,
		Token:       c.Token,
		PublicPort:  c.PublicPort,
		AllowedIPs:  c.AllowedIPs,
		DisplayName: c.DisplayName,
		CreatedAt:   c.CreatedAt,
		UpdatedAt:   c.UpdatedAt,
		Online:      st.Online,
		BytesIn:     st.BytesIn,
		BytesOut:    st.BytesOut,
		Streams:     st.Streams,
	}
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(v)
}
