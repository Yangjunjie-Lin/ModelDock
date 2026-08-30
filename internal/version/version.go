package version

import "fmt"

const (
	// ProductName is the current product brand. CompatibilityName remains the
	// externally visible legacy name on existing API fields and runtime paths.
	ProductName       = "ModelDock"
	CompatibilityName = "RelayDock"
)

// These values are variables so release builds can inject immutable source
// metadata with -ldflags -X. Development builds intentionally retain explicit
// values instead of reading mutable environment variables at runtime.
var (
	Current   = "3.0.0-beta.2"
	Commit    = "unknown"
	BuildTime = "unknown"
)

type Info struct {
	Product           string `json:"product"`
	CompatibilityName string `json:"compatibility_name"`
	Version           string `json:"version"`
	Commit            string `json:"commit"`
	BuildTime         string `json:"build_time"`
}

func Metadata() Info {
	return Info{
		Product:           ProductName,
		CompatibilityName: CompatibilityName,
		Version:           Current,
		Commit:            Commit,
		BuildTime:         BuildTime,
	}
}

func String() string {
	return fmt.Sprintf("%s %s (%s compatibility; commit %s; built %s)",
		ProductName, Current, CompatibilityName, Commit, BuildTime)
}
