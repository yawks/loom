// Package core provides the core interfaces and types for chat providers.
package core

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"sync"
	"time"
)

// ProviderInfo represents information about a provider.
type ProviderInfo struct {
	ID           string                 `json:"id"`           // Unique identifier (e.g., "whatsapp", "mock")
	Name         string                 `json:"name"`         // Display name (e.g., "WhatsApp", "Mock")
	Description  string                 `json:"description"`  // Description of the provider
	Config       ProviderConfig         `json:"config"`       // Current configuration
	IsActive     bool                   `json:"isActive"`     // Whether the provider is currently active
	ConfigSchema map[string]interface{} `json:"configSchema"` // Schema for configuration fields
}

// ProviderFactory is a function that creates a new provider instance.
type ProviderFactory func() Provider

// ProviderManager manages multiple providers.
type ProviderManager struct {
	providers map[string]Provider
	factories map[string]ProviderFactory
	infos     map[string]ProviderInfo
	mu        sync.RWMutex
	activeID  string // ID of the currently active provider
}

// NewProviderManager creates a new provider manager.
func NewProviderManager() *ProviderManager {
	return &ProviderManager{
		providers: make(map[string]Provider),
		factories: make(map[string]ProviderFactory),
		infos:     make(map[string]ProviderInfo),
	}
}

// RegisterProvider registers a provider factory.
func (pm *ProviderManager) RegisterProvider(id string, info ProviderInfo, factory ProviderFactory) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.factories[id] = factory
	pm.infos[id] = info
}

// GetAvailableProviders returns a list of all available providers.
func (pm *ProviderManager) GetAvailableProviders() []ProviderInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	providers := make([]ProviderInfo, 0, len(pm.infos))
	for _, info := range pm.infos {
		providers = append(providers, info)
	}
	return providers
}

// GetConfiguredProviders returns a list of configured (initialized) providers.
func (pm *ProviderManager) GetConfiguredProviders() []ProviderInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	providers := make([]ProviderInfo, 0)
	for id, provider := range pm.providers {
		info := pm.infos[id]
		info.Config = provider.GetConfig()
		info.IsActive = (id == pm.activeID)
		providers = append(providers, info)
	}
	return providers
}

// CreateProvider creates a new provider instance and saves it to the database.
func (pm *ProviderManager) CreateProvider(id string, config ProviderConfig) (Provider, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	factory, ok := pm.factories[id]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", id)
	}

	// If provider already exists, disconnect and replace it
	if existing, exists := pm.providers[id]; exists {
		_ = existing.Disconnect()
		delete(pm.providers, id)
		if pm.activeID == id {
			pm.activeID = ""
		}
	}

	provider := factory()
	if err := provider.Init(config); err != nil {
		return nil, fmt.Errorf("failed to initialize provider: %w", err)
	}

	pm.providers[id] = provider

	// Save configuration to database
	if err := pm.saveProviderConfig(id, config, false); err != nil {
		// Log error but don't fail the creation
		fmt.Printf("Warning: Failed to save provider config to database: %v\n", err)
	}

	return provider, nil
}

// AddProvider adds a provider instance to the manager without saving to database.
// This is useful for default providers like MockProvider.
func (pm *ProviderManager) AddProvider(id string, provider Provider) {
	pm.mu.Lock()
	defer pm.mu.Unlock()
	pm.providers[id] = provider
}

// GetProvider returns a provider by ID.
func (pm *ProviderManager) GetProvider(id string) (Provider, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	provider, ok := pm.providers[id]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", id)
	}
	return provider, nil
}

// SetActiveProvider sets the active provider and updates the database.
func (pm *ProviderManager) SetActiveProvider(id string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, ok := pm.providers[id]; !ok {
		return fmt.Errorf("provider not found: %s", id)
	}

	// Update active status in database
	// First, set all providers to inactive
	if db.DB != nil {
		db.DB.Model(&models.ProviderConfiguration{}).Where("is_active = ?", true).Update("is_active", false)
		// Then set the new active provider
		db.DB.Model(&models.ProviderConfiguration{}).Where("provider_id = ?", id).Update("is_active", true)
	}

	pm.activeID = id
	return nil
}

// GetActiveProvider returns the currently active provider.
func (pm *ProviderManager) GetActiveProvider() (Provider, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.activeID == "" {
		return nil, fmt.Errorf("no active provider")
	}

	provider, ok := pm.providers[pm.activeID]
	if !ok {
		return nil, fmt.Errorf("active provider not found: %s", pm.activeID)
	}
	return provider, nil
}

// RemoveProvider removes a provider and deletes it from the database.
func (pm *ProviderManager) RemoveProvider(id string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	provider, ok := pm.providers[id]
	if !ok {
		return fmt.Errorf("provider not found: %s", id)
	}

	// Disconnect if active
	if id == pm.activeID {
		provider.Disconnect()
		pm.activeID = ""
	}

	delete(pm.providers, id)

	// Delete provider configuration and all associated data from database
	if db.DB != nil {
		// Delete provider configuration (use Unscoped to force delete, not soft delete)
		db.DB.Unscoped().Where("provider_id = ?", id).Delete(&models.ProviderConfiguration{})

		// Delete all data associated with this provider
		// Find all LinkedAccounts for this provider (use Unscoped to include soft-deleted)
		var linkedAccounts []models.LinkedAccount
		if err := db.DB.Unscoped().Where("protocol = ?", id).Find(&linkedAccounts).Error; err == nil {
			for _, account := range linkedAccounts {
				// Find all conversations for this linked account
				var conversations []models.Conversation
				if err := db.DB.Where("linked_account_id = ?", account.ID).Find(&conversations).Error; err == nil {
					for _, conv := range conversations {
						// Delete all reactions for messages in this conversation
						var messages []models.Message
						if err := db.DB.Unscoped().Where("conversation_id = ?", conv.ID).Find(&messages).Error; err == nil {
							for _, msg := range messages {
								db.DB.Unscoped().Where("message_id = ?", msg.ID).Delete(&models.Reaction{})
								db.DB.Unscoped().Where("message_id = ?", msg.ID).Delete(&models.MessageReceipt{})
							}
						}
						// Delete all messages for this conversation (use Unscoped for hard delete)
						db.DB.Unscoped().Where("conversation_id = ?", conv.ID).Delete(&models.Message{})
						// Delete group participants
						db.DB.Unscoped().Where("conversation_id = ?", conv.ID).Delete(&models.GroupParticipant{})
						// Delete the conversation
						db.DB.Unscoped().Delete(&conv)
					}
				}

				// Get the MetaContactID before deleting the linked account
				metaContactID := account.MetaContactID

				// Delete the linked account (use Unscoped for hard delete)
				db.DB.Unscoped().Delete(&account)

				// Check if the MetaContact has any remaining LinkedAccounts
				var remainingAccounts []models.LinkedAccount
				if err := db.DB.Where("meta_contact_id = ?", metaContactID).Find(&remainingAccounts).Error; err == nil {
					if len(remainingAccounts) == 0 {
						// No more linked accounts, delete the MetaContact
						db.DB.Unscoped().Where("id = ?", metaContactID).Delete(&models.MetaContact{})
					}
				}
			}
		}
	}

	return nil
}

// saveProviderConfig saves a provider configuration to the database.
func (pm *ProviderManager) saveProviderConfig(id string, config ProviderConfig, isActive bool) error {
	if db.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Convert config to JSON
	configJSON, err := json.Marshal(config)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Save or update configuration
	var providerConfig models.ProviderConfiguration
	result := db.DB.Where("provider_id = ?", id).First(&providerConfig)

	if result.Error != nil {
		// Create new
		providerConfig = models.ProviderConfiguration{
			ProviderID: id,
			ConfigJSON: string(configJSON),
			IsActive:   isActive,
		}
		return db.DB.Create(&providerConfig).Error
	}

	// Update existing
	providerConfig.ConfigJSON = string(configJSON)
	providerConfig.IsActive = isActive
	providerConfig.UpdatedAt = time.Now()
	return db.DB.Save(&providerConfig).Error
}

// LoadProviderConfigs loads all provider configurations from the database.
func (pm *ProviderManager) LoadProviderConfigs() ([]models.ProviderConfiguration, error) {
	if db.DB == nil {
		return nil, fmt.Errorf("database not initialized")
	}

	var configs []models.ProviderConfiguration
	if err := db.DB.Find(&configs).Error; err != nil {
		return nil, fmt.Errorf("failed to load provider configs: %w", err)
	}

	return configs, nil
}

// RestoreProvider restores a provider from database configuration.
func (pm *ProviderManager) RestoreProvider(config models.ProviderConfiguration) (Provider, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	factory, ok := pm.factories[config.ProviderID]
	if !ok {
		return nil, fmt.Errorf("provider not found: %s", config.ProviderID)
	}

	// Parse config JSON
	var providerConfig ProviderConfig
	if err := json.Unmarshal([]byte(config.ConfigJSON), &providerConfig); err != nil {
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}

	// Create provider instance
	provider := factory()
	if err := provider.Init(providerConfig); err != nil {
		return nil, fmt.Errorf("failed to initialize provider: %w", err)
	}

	pm.providers[config.ProviderID] = provider

	// Set as active if needed
	if config.IsActive {
		pm.activeID = config.ProviderID
	}

	return provider, nil
}
