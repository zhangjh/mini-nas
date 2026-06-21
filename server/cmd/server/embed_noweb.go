//go:build !withweb

package main

import (
	"net/http"
)

func staticHandler() http.Handler {
	return http.NotFoundHandler()
}

func spaFallback(next http.Handler) http.Handler {
	return next
}
