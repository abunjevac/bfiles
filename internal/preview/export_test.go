package preview

// Sanitize exposes sanitize to the external tests.
func Sanitize(s string, noBg bool) string {
	return sanitize(s, noBg)
}
