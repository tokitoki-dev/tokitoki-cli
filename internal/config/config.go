package config

const (
	// DataDirName is the hidden directory created under the user's home
	// directory for shared TokiToki agent state. The same path is used on
	// macOS, Windows, and Linux so every native front-end resolves it the
	// same way: filepath.Join(os.UserHomeDir(), config.DataDirName).
	DataDirName = ".tokitoki"
)
