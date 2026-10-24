package ui

// Key names as reported by tea.KeyPressMsg.String(), used by overlays that handle
// keys directly instead of through the keymap.
const (
	keyEnter     = "enter"
	keyEsc       = "esc"
	keyCtrlC     = "ctrl+c"
	keyUp        = "up"
	keyDown      = "down"
	keyLeft      = "left"
	keyRight     = "right"
	keyPgUp      = "pgup"
	keyPgDown    = "pgdown"
	keyHome      = "home"
	keyEnd       = "end"
	keySpace     = "space"
	keyTab       = "tab"
	keyShiftTab  = "shift+tab"
	keyBackspace = "backspace"
	keyDelete    = "delete"
	keyCtrlD     = "ctrl+d"
	keyCtrlA     = "ctrl+a"
	keyCtrlE     = "ctrl+e"
)

// isCancel reports keys that close a dialog.
func isCancel(key string) bool {
	return key == keyEsc || key == keyCtrlC
}
