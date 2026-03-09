package cli

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/viper"
)

// Config holds the CLI configuration
type Config struct {
	ServerURL   string `mapstructure:"server_url"`
	Token       string `mapstructure:"token"`
	WatchFolder string `mapstructure:"watch_folder"`
}

// InitConfig initializes the configuration
func InitConfig() {
	var home string

	if cfgFile != "" {
		// Use config file from the flag.
		viper.SetConfigFile(cfgFile)
	} else {
		// Find home directory.
		var err error
		home, err = os.UserHomeDir()
		if err != nil {
			fmt.Println(err)
			// Don't exit, just don't load config from home
		} else {
			// Search config in home directory with name ".quill" (without extension).
			viper.AddConfigPath(home)
			viper.SetConfigType("yaml")
			viper.SetConfigName(".quill")
		}
	}

	viper.SetEnvPrefix("QUILL")
	viper.AutomaticEnv()
	_ = viper.BindEnv("server_url", "QUILL_SERVER_URL")
	_ = viper.BindEnv("token", "QUILL_TOKEN")
	_ = viper.BindEnv("watch_folder", "QUILL_WATCH_FOLDER")

	// Try to read config from primary location.
	if err := viper.ReadInConfig(); err == nil {
		return
	}

}

// SaveConfig saves the configuration to ~/.quill.yaml and returns the path
func SaveConfig(serverURL, token, watchFolder string) (string, error) {
	if serverURL != "" {
		viper.Set("server_url", serverURL)
	}
	if token != "" {
		viper.Set("token", token)
	}
	if watchFolder != "" {
		viper.Set("watch_folder", watchFolder)
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	configPath := filepath.Join(home, ".quill.yaml")
	return configPath, viper.WriteConfigAs(configPath)
}

// GetConfig returns the current configuration
func GetConfig() *Config {
	return &Config{
		ServerURL:   viper.GetString("server_url"),
		Token:       viper.GetString("token"),
		WatchFolder: viper.GetString("watch_folder"),
	}
}
