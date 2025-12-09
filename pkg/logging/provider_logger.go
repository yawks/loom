// Package logging provides logging functionality for providers.
// Each provider instance gets its own log file stored in the application's config directory.
package logging

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// ProviderLogger provides logging functionality for providers
// Each provider instance gets its own log file
type ProviderLogger struct {
	providerID string
	instanceID string
	logFile    *os.File
	logger     *log.Logger
	mu         sync.Mutex
	logDir     string
}

var (
	loggers   = make(map[string]*ProviderLogger)
	loggersMu sync.RWMutex
)

// GetLogger returns a logger instance for a specific provider
// If the logger doesn't exist, it creates a new one
func GetLogger(providerID, instanceID string) (*ProviderLogger, error) {
	key := fmt.Sprintf("%s-%s", providerID, instanceID)

	loggersMu.RLock()
	if logger, exists := loggers[key]; exists {
		loggersMu.RUnlock()
		return logger, nil
	}
	loggersMu.RUnlock()

	// Create new logger
	loggersMu.Lock()
	defer loggersMu.Unlock()

	// Double-check after acquiring write lock
	if logger, exists := loggers[key]; exists {
		return logger, nil
	}

	// Get config directory
	configDir, err := os.UserConfigDir()
	if err != nil {
		return nil, fmt.Errorf("failed to get config directory: %w", err)
	}

	// Create log directory: configDir/Loom/logs
	logDir := filepath.Join(configDir, "Loom", "logs")
	if err := os.MkdirAll(logDir, 0750); err != nil {
		return nil, fmt.Errorf("failed to create log directory: %w", err)
	}

	// Create log file: configDir/Loom/logs/provider-instance.log
	logFileName := fmt.Sprintf("%s-%s.log", providerID, instanceID)
	logFilePath := filepath.Join(logDir, logFileName)

	// Open log file in append mode
	logFile, err := os.OpenFile(logFilePath, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0640)
	if err != nil {
		return nil, fmt.Errorf("failed to open log file: %w", err)
	}

	// Create logger that writes to both file and stdout (for development)
	multiWriter := io.MultiWriter(logFile, os.Stdout)
	logger := log.New(multiWriter, fmt.Sprintf("[%s] ", key), log.LstdFlags|log.Lmicroseconds)

	pl := &ProviderLogger{
		providerID: providerID,
		instanceID: instanceID,
		logFile:    logFile,
		logger:     logger,
		logDir:     logDir,
	}

	loggers[key] = pl
	return pl, nil
}

// Log writes a log message with the given format and arguments
func (pl *ProviderLogger) Log(format string, args ...interface{}) {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.logger != nil {
		pl.logger.Printf(format, args...)
	}
}

// Logf is an alias for Log for consistency with fmt.Printf
func (pl *ProviderLogger) Logf(format string, args ...interface{}) {
	pl.Log(format, args...)
}

// Close closes the log file
func (pl *ProviderLogger) Close() error {
	pl.mu.Lock()
	defer pl.mu.Unlock()

	if pl.logFile != nil {
		err := pl.logFile.Close()
		pl.logFile = nil
		pl.logger = nil

		// Remove from map
		key := fmt.Sprintf("%s-%s", pl.providerID, pl.instanceID)
		loggersMu.Lock()
		delete(loggers, key)
		loggersMu.Unlock()

		return err
	}
	return nil
}

// CleanupOldLogs removes log files older than the specified number of days
func CleanupOldLogs(days int) error {
	configDir, err := os.UserConfigDir()
	if err != nil {
		return fmt.Errorf("failed to get config directory: %w", err)
	}

	logDir := filepath.Join(configDir, "Loom", "logs")

	// Read directory
	entries, err := os.ReadDir(logDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil // Directory doesn't exist, nothing to clean
		}
		return fmt.Errorf("failed to read log directory: %w", err)
	}

	cutoffTime := time.Now().AddDate(0, 0, -days)
	removed := 0

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}

		// Only process .log files
		if filepath.Ext(entry.Name()) != ".log" {
			continue
		}

		info, err := entry.Info()
		if err != nil {
			continue
		}

		// Remove old files
		if info.ModTime().Before(cutoffTime) {
			filePath := filepath.Join(logDir, entry.Name())
			if err := os.Remove(filePath); err != nil {
				log.Printf("Failed to remove old log file %s: %v", filePath, err)
			} else {
				removed++
			}
		}
	}

	if removed > 0 {
		log.Printf("Cleaned up %d old log file(s) older than %d days", removed, days)
	}

	return nil
}

// CloseAllLoggers closes all open loggers
func CloseAllLoggers() {
	loggersMu.Lock()
	defer loggersMu.Unlock()

	for key, logger := range loggers {
		if err := logger.Close(); err != nil {
			log.Printf("Error closing logger %s: %v", key, err)
		}
	}

	loggers = make(map[string]*ProviderLogger)
}
