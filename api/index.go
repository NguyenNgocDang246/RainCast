// Package handler is the forecast API as a Vercel Go function; vercel.json
// sends every /api/* request here. Everything is in app.Serverless: Vercel
// wraps this file in a package outside the module's tree, which may only
// import raincast's non-internal packages.
package handler

import (
	"net/http"

	"raincast/app"
)

var api = app.Serverless()

// Handler serves every /api/* request.
func Handler(w http.ResponseWriter, r *http.Request) {
	api.ServeHTTP(w, r)
}
