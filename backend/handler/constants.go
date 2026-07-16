package handler

import "strconv"

const (
	RoleAdmin  = "admin"
	RoleEditor = "editor"
	RoleViewer = "viewer"
)

const (
	DefaultPageLimit   = 100
	MaxPageLimit       = 500
	DefaultLogLimit    = 50
	MaxLogLimit        = 1000
	DefaultExportLimit = 100000
	MaxExportLimit     = 100000
	DefaultParserLimit = 10000
	MaxParserLimit     = 10000
	DefaultAdminLimit  = 100
)

func parseIDParam(id string) (int64, error) {
	return strconv.ParseInt(id, 10, 64)
}
