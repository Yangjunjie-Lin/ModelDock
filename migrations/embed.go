package migrations

import _ "embed"

// Migration is one immutable, ordered database migration.  Version values are
// persisted by the store in schema_migrations only after the SQL transaction
// commits successfully.
type Migration struct {
	Version int64
	Name    string
	SQL     string
}

//go:embed 0001_core.sql
var coreSchema string

//go:embed 0002_v2.sql
var v2Schema string

//go:embed 0003_v2_statuses.sql
var v2StatusesSchema string

//go:embed 0004_project_route_soft_delete.sql
var projectRouteSoftDeleteSchema string

//go:embed 0005_openai_compatible_providers.sql
var openAICompatibleProvidersSchema string

//go:embed 0006_modeldock.sql
var modelDockSchema string

// All is ordered oldest to newest.  Never edit a released migration; append a
// new entry instead so checksum validation can detect binary/schema drift.
var All = []Migration{
	{Version: 1, Name: "core", SQL: coreSchema},
	{Version: 2, Name: "v2", SQL: v2Schema},
	{Version: 3, Name: "v2_statuses", SQL: v2StatusesSchema},
	{Version: 4, Name: "project_route_soft_delete", SQL: projectRouteSoftDeleteSchema},
	{Version: 5, Name: "openai_compatible_providers", SQL: openAICompatibleProvidersSchema},
	{Version: 6, Name: "modeldock", SQL: modelDockSchema},
}
