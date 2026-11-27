// Package api is the JSON API used by the forwarder mobile app.
package api

import (
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/itsmepava/container/internal/app"
)

type API struct{ app *app.App }

func New(a *app.App) *API { return &API{app: a} }

func (x *API) Handler() http.Handler {
	r := chi.NewRouter()
	return r
}
