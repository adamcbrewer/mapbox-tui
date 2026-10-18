package shared

import _ "embed"

//go:embed tools.json
var Tools []byte

//go:embed control_tools.json
var ControlTools []byte
