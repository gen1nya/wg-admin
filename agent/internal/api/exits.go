package api

import "net/http"

func (s *Server) listExits(w http.ResponseWriter, r *http.Request) {
	exits, err := s.Store.ListExits(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, exits)
}

func (s *Server) listMarks(w http.ResponseWriter, r *http.Request) {
	marks, err := s.Store.ListMarks(r.Context())
	if err != nil {
		writeErr(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, marks)
}
