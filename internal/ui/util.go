package ui

// onOff renders b as "on"/"off", for a status line reporting a toggle's
// current state (e.g. hide-done filtering in appendConfigRows).
func onOff(b bool) string {
	if b {
		return "on"
	}
	return "off"
}
