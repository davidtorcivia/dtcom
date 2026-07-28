package server

import "net/http"

// registerAdmin wires the session-cookie-authenticated admin UI. (Task 9.4)
func registerAdmin(mux *http.ServeMux, d *Deps) {
	// Placeholder — admin routes are added in Task 9.4. Defining the function
	// here lets the public+API mux wire up and test independently.
	_ = mux
	_ = d
}

// requireAuth is the session-cookie guard used by admin handlers. (Task 9.4)
func (d *Deps) requireAuth(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if _, ok := d.Auth.SessionUser(r); !ok {
			http.Redirect(w, r, "/admin/login", http.StatusSeeOther)
			return
		}
		next(w, r)
	}
}
