package promptfmt

func WithStartupPrompt(startupPrompt, message string) string {
	if startupPrompt == "" {
		return message
	}
	return "Startup prompt:\n" + startupPrompt + "\n\nFacilitator message:\n" + message
}
