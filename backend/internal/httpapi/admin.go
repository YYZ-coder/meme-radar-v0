package httpapi

import (
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/meme-radar/radar/internal/api"
	"github.com/meme-radar/radar/internal/smlist"
)

// authorized compares the bearer token in constant time (D106).
func (s *server) authorized(r *http.Request) bool {
	given, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer ")
	return ok && s.opt.AdminToken != "" && subtle.ConstantTimeCompare([]byte(given), []byte(s.opt.AdminToken)) == 1
}

// admin guards h before any routing detail leaks; disabled admin routes get 401.
func (s *server) admin(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if s.opt.AdminDisabled {
			s.fail(w, r, api.Errorf(http.StatusUnauthorized, "unauthorized", "", "admin API is disabled"))
			return
		}
		if !s.authorized(r) {
			s.fail(w, r, api.Errorf(http.StatusForbidden, "forbidden", "", "admin token required"))
			return
		}
		h(w, r)
	}
}

func (s *server) adminRoutes(mux *http.ServeMux) {
	const p = "/api/v1/admin/"
	mux.HandleFunc(p, s.admin(func(w http.ResponseWriter, r *http.Request) {
		s.fail(w, r, api.Errorf(http.StatusNotFound, "not_found", "", "no such admin endpoint"))
	}))
	mux.HandleFunc("GET "+p+"health", s.admin(func(w http.ResponseWriter, r *http.Request) {
		v, err := s.b.Health(r.Context())
		s.reply(w, r, http.StatusOK, v, err)
	}))
	mux.HandleFunc("POST "+p+"parse-failures/{id}/retry", s.admin(func(w http.ResponseWriter, r *http.Request) {
		f, err := s.b.RetryFailure(r.Context(), r.PathValue("id"))
		if err != nil {
			s.fail(w, r, err)
			return
		}
		s.json(w, http.StatusAccepted, f)
	}))
	mux.HandleFunc("POST "+p+"smart-money/imports", s.admin(s.createImport))
	mux.HandleFunc("GET "+p+"smart-money/imports/{id}", s.admin(func(w http.ResponseWriter, r *http.Request) {
		rep, err := s.b.Import(r.Context(), r.PathValue("id"))
		s.reply(w, r, http.StatusOK, rep, err)
	}))
	mux.HandleFunc("POST "+p+"smart-money/imports/{id}/apply", s.admin(func(w http.ResponseWriter, r *http.Request) {
		change, err := s.b.ApplyImport(r.Context(), r.PathValue("id"))
		s.accepted(w, r, change, err)
	}))
	mux.HandleFunc("GET "+p+"smart-money/list-builds/{version}", s.admin(func(w http.ResponseWriter, r *http.Request) {
		st, err := s.b.ListBuild(r.Context(), r.PathValue("version"))
		s.reply(w, r, http.StatusOK, st, err)
	}))
	mux.HandleFunc("POST "+p+"smart-money/list-builds/{version}/retry", s.admin(func(w http.ResponseWriter, r *http.Request) {
		st, err := s.b.RetryListBuild(r.Context(), r.PathValue("version"))
		if err == nil && st.BuildStatus == "activated" {
			s.reply(w, r, http.StatusOK, st, nil) // nothing to retry
			return
		}
		s.accepted(w, r, st, err)
	}))
	mux.HandleFunc("GET "+p+"smart-money/members", s.admin(s.members))
	mux.HandleFunc("GET "+p+"smart-money/exclusions", s.admin(func(w http.ResponseWriter, r *http.Request) {
		chain, err := queryChain(r)
		if err != nil {
			s.fail(w, r, err)
			return
		}
		v, err := s.b.Exclusions(r.Context(), chain)
		s.reply(w, r, http.StatusOK, v, err)
	}))
	mux.HandleFunc("POST "+p+"smart-money/exclusions", s.admin(s.exclude))
	mux.HandleFunc("DELETE "+p+"smart-money/exclusions/{chain}/{address}", s.admin(s.walletChange(s.b.Restore)))
	mux.HandleFunc("DELETE "+p+"smart-money/members/{chain}/{address}", s.admin(s.walletChange(s.b.RemoveMember)))
}

// reply writes v with status, or the error.
func (s *server) reply(w http.ResponseWriter, r *http.Request, status int, v any, err error) {
	if err != nil {
		s.fail(w, r, err)
		return
	}
	if err := writeDataTime(w, r); err != nil {
		s.fail(w, r, err)
		return
	}
	s.json(w, status, v)
}

// accepted answers an asynchronous list change: 202 with Retry-After for polling.
func (s *server) accepted(w http.ResponseWriter, r *http.Request, v any, err error) {
	if err == nil {
		w.Header().Set("Retry-After", "2")
	}
	s.reply(w, r, http.StatusAccepted, v, err)
}

func tooLarge() *api.Error {
	return api.Errorf(http.StatusRequestEntityTooLarge, "file_too_large", "file", "file exceeds 5MB")
}

func (s *server) createImport(w http.ResponseWriter, r *http.Request) {
	// Allow multipart framing around a 5MB file; the file size itself is checked below.
	r.Body = http.MaxBytesReader(w, r.Body, smlist.MaxBytes+1<<20)
	if err := r.ParseMultipartForm(smlist.MaxBytes + 1<<20); err != nil {
		var mb *http.MaxBytesError
		if errors.As(err, &mb) {
			s.fail(w, r, tooLarge())
			return
		}
		s.fail(w, r, api.Errorf(http.StatusBadRequest, "invalid_file", "file", "multipart form with a file field required"))
		return
	}
	file, header, err := r.FormFile("file")
	if err != nil {
		s.fail(w, r, api.Errorf(http.StatusBadRequest, "invalid_file", "file", "file required"))
		return
	}
	defer file.Close()
	if header.Size > smlist.MaxBytes {
		s.fail(w, r, tooLarge())
		return
	}
	content, err := io.ReadAll(file)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	rep, err := s.b.CreateImport(r.Context(), header.Filename, content)
	s.reply(w, r, http.StatusCreated, rep, err)
}
