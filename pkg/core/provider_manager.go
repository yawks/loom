// Package core provides the core interfaces and types for chat providers.
package core

import (
	"Loom/pkg/db"
	"Loom/pkg/models"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// ProviderInfo represents information about a provider.
type ProviderInfo struct {
	ID           string                 `json:"id"`           // Provider type identifier (e.g., "whatsapp", "mock")
	InstanceID   string                 `json:"instanceId"`   // Unique instance identifier (e.g., "whatsapp-1", "whatsapp-2")
	InstanceName string                 `json:"instanceName"` // Display name for this instance (e.g., "WhatsApp Personal")
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
	providers        map[string]Provider        // Key: InstanceID (e.g., "whatsapp-1")
	factories        map[string]ProviderFactory // Key: ProviderID (e.g., "whatsapp")
	infos            map[string]ProviderInfo    // Key: ProviderID (e.g., "whatsapp")
	mu               sync.RWMutex
	activeInstanceID string // InstanceID of the currently active provider (e.g., "whatsapp-1")
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
	fmt.Printf("ProviderManager: Registered provider %s (name: %s)\n", id, info.Name)
}

// GetAvailableProviders returns a list of all available providers.
func (pm *ProviderManager) GetAvailableProviders() []ProviderInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	fmt.Printf("ProviderManager.GetAvailableProviders: infos count: %d, providers count: %d\n", len(pm.infos), len(pm.providers))

	providers := make([]ProviderInfo, 0, len(pm.infos))
	for id, info := range pm.infos {
		// Make a copy to avoid modifying the original
		providerInfo := info
		// For available providers, we don't check active status here
		// Active status is only relevant for configured instances
		providerInfo.IsActive = false
		providers = append(providers, providerInfo)
		fmt.Printf("ProviderManager.GetAvailableProviders: added provider %s (name: %s)\n", id, info.Name)
	}

	// Sort providers by name (alphabetically)
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Name < providers[j].Name
	})

	fmt.Printf("ProviderManager.GetAvailableProviders: returning %d providers (sorted alphabetically)\n", len(providers))
	return providers
}

// GetConfiguredProviders returns a list of configured (initialized) providers.
func (pm *ProviderManager) GetConfiguredProviders() []ProviderInfo {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	fmt.Printf("ProviderManager.GetConfiguredProviders: providers count: %d, infos count: %d\n", len(pm.providers), len(pm.infos))

	providers := make([]ProviderInfo, 0)
	for instanceID, provider := range pm.providers {
		// Extract providerID from instanceID (e.g., "whatsapp-1" -> "whatsapp")
		parts := strings.Split(instanceID, "-")
		if len(parts) < 2 {
			fmt.Printf("ProviderManager.GetConfiguredProviders: WARNING - invalid instanceID format: %s\n", instanceID)
			continue
		}
		providerID := strings.Join(parts[:len(parts)-1], "-")

		info, exists := pm.infos[providerID]
		if !exists {
			// If info doesn't exist, create a basic one
			fmt.Printf("ProviderManager.GetConfiguredProviders: info not found for %s, creating basic one\n", providerID)
			info = ProviderInfo{
				ID:   providerID,
				Name: providerID,
			}
		}

		// Load instance name from database
		var config models.ProviderConfiguration
		if db.DB != nil {
			db.DB.Where("instance_id = ?", instanceID).First(&config)
		}

		info.InstanceID = instanceID
		info.InstanceName = config.InstanceName
		if info.InstanceName == "" {
			info.InstanceName = instanceID
		}
		info.Config = provider.GetConfig()
		info.IsActive = (instanceID == pm.activeInstanceID)
		providers = append(providers, info)
		fmt.Printf("ProviderManager.GetConfiguredProviders: added configured provider %s (instance: %s, name: %s, active: %v)\n", providerID, instanceID, info.InstanceName, info.IsActive)
	}

	// Sort providers by name (alphabetically)
	sort.Slice(providers, func(i, j int) bool {
		return providers[i].Name < providers[j].Name
	})

	fmt.Printf("ProviderManager.GetConfiguredProviders: returning %d providers (sorted alphabetically)\n", len(providers))
	return providers
}

// CreateProvider creates a new provider instance and saves it to the database.
// instanceName is optional - if empty, a default name will be generated.
// If existingInstanceID is provided and not empty (for edit mode), it will be used instead of generating a new one.
func (pm *ProviderManager) CreateProvider(providerID string, config ProviderConfig, instanceName string, existingInstanceID string) (string, Provider, error) {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	factory, ok := pm.factories[providerID]
	if !ok {
		return "", nil, fmt.Errorf("provider not found: %s", providerID)
	}

	// Use existing instanceID if provided (edit mode), otherwise generate a new one
	var instanceID string
	if existingInstanceID != "" {
		instanceID = existingInstanceID
		fmt.Printf("ProviderManager.CreateProvider: Using existing instanceID %s (edit mode)\n", instanceID)
	} else {
		instanceID = pm.generateInstanceID(providerID)
		fmt.Printf("ProviderManager.CreateProvider: Generated new instanceID %s (create mode)\n", instanceID)
	}

	// If instanceName is empty, generate a default name
	if instanceName == "" {
		instanceName = fmt.Sprintf("%s %d", providerID, len(pm.getInstancesForProvider(providerID))+1)
	}

	// If provider instance already exists, disconnect and replace it
	if existing, exists := pm.providers[instanceID]; exists {
		fmt.Printf("ProviderManager.CreateProvider: Disconnecting existing provider instance %s\n", instanceID)
		_ = existing.Disconnect()
		delete(pm.providers, instanceID)
		if pm.activeInstanceID == instanceID {
			pm.activeInstanceID = ""
		}
	}

	// Add instanceID to config so provider can use it for isolated storage
	if config == nil {
		config = make(ProviderConfig)
	}
	config["_instance_id"] = instanceID

	provider := factory()
	if err := provider.Init(config); err != nil {
		return "", nil, fmt.Errorf("failed to initialize provider: %w", err)
	}

	pm.providers[instanceID] = provider

	// Save configuration to database
	if err := pm.saveProviderConfig(providerID, instanceID, instanceName, config, false); err != nil {
		// Log error but don't fail the creation
		fmt.Printf("Warning: Failed to save provider config to database: %v\n", err)
	}

	return instanceID, provider, nil
}

// generateInstanceID generates a unique instance ID for a provider
func (pm *ProviderManager) generateInstanceID(providerID string) string {
	instances := pm.getInstancesForProvider(providerID)
	instanceNum := len(instances) + 1
	return fmt.Sprintf("%s-%d", providerID, instanceNum)
}

// getInstancesForProvider returns all instance IDs for a given provider type
func (pm *ProviderManager) getInstancesForProvider(providerID string) []string {
	instances := []string{}
	for instanceID := range pm.providers {
		if strings.HasPrefix(instanceID, providerID+"-") {
			instances = append(instances, instanceID)
		}
	}
	return instances
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

// SetActiveProvider sets the active provider instance and updates the database.
func (pm *ProviderManager) SetActiveProvider(instanceID string) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if _, ok := pm.providers[instanceID]; !ok {
		return fmt.Errorf("provider instance not found: %s", instanceID)
	}

	// Update active status in database
	// First, set all providers to inactive
	if db.DB != nil {
		db.DB.Model(&models.ProviderConfiguration{}).Where("is_active = ?", true).Update("is_active", false)
		// Then set the new active provider instance
		db.DB.Model(&models.ProviderConfiguration{}).Where("instance_id = ?", instanceID).Update("is_active", true)
	}

	pm.activeInstanceID = instanceID
	return nil
}

// GetActiveProvider returns the currently active provider instance.
func (pm *ProviderManager) GetActiveProvider() (Provider, error) {
	pm.mu.RLock()
	defer pm.mu.RUnlock()

	if pm.activeInstanceID == "" {
		return nil, fmt.Errorf("no active provider")
	}

	provider, ok := pm.providers[pm.activeInstanceID]
	if !ok {
		return nil, fmt.Errorf("active provider not found: %s", pm.activeInstanceID)
	}
	return provider, nil
}

// RemoveProvider removes a provider instance and deletes it from the database.
func (pm *ProviderManager) RemoveProvider(instanceID string) error {
	fmt.Printf("ProviderManager.RemoveProvider: Called with instanceID=%s\n", instanceID)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	provider, ok := pm.providers[instanceID]
	if !ok {
		fmt.Printf("ProviderManager.RemoveProvider: ERROR - provider instance not found: %s (available instances: %v)\n", instanceID, getMapKeys(pm.providers))
		return fmt.Errorf("provider instance not found: %s", instanceID)
	}
	fmt.Printf("ProviderManager.RemoveProvider: Found provider instance %s\n", instanceID)

	// Disconnect if active
	if instanceID == pm.activeInstanceID {
		provider.Disconnect()
		pm.activeInstanceID = ""
	}

	delete(pm.providers, instanceID)

	// Delete provider configuration and all associated data from database
	if db.DB != nil {
		// Delete provider configuration (use Unscoped to force delete, not soft delete)
		db.DB.Unscoped().Where("instance_id = ?", instanceID).Delete(&models.ProviderConfiguration{})

		// Delete all data associated with this provider instance
		// Find all LinkedAccounts for this provider instance (use Unscoped to include soft-deleted)
		var linkedAccounts []models.LinkedAccount
		if err := db.DB.Unscoped().Where("provider_instance_id = ?", instanceID).Find(&linkedAccounts).Error; err == nil {
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

	fmt.Printf("ProviderManager.RemoveProvider: Successfully removed provider instance %s\n", instanceID)
	return nil
}

// getMapKeys returns all keys from a map[string]Provider
func getMapKeys(m map[string]Provider) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	return keys
}

// saveProviderConfig saves a provider configuration to the database.
func (pm *ProviderManager) saveProviderConfig(providerID, instanceID, instanceName string, config ProviderConfig, isActive bool) error {
	if db.DB == nil {
		return fmt.Errorf("database not initialized")
	}

	// Create a copy of config without internal fields like _instance_id
	configToSave := make(ProviderConfig)
	for k, v := range config {
		// Skip internal fields that start with underscore
		if !strings.HasPrefix(k, "_") {
			configToSave[k] = v
		}
	}

	// Convert config to JSON
	configJSON, err := json.Marshal(configToSave)
	if err != nil {
		return fmt.Errorf("failed to marshal config: %w", err)
	}

	// Save or update configuration
	var providerConfig models.ProviderConfiguration
	result := db.DB.Where("instance_id = ?", instanceID).First(&providerConfig)

	if result.Error != nil {
		// Create new
		providerConfig = models.ProviderConfiguration{
			ProviderID:   providerID,
			InstanceID:   instanceID,
			InstanceName: instanceName,
			ConfigJSON:   string(configJSON),
			IsActive:     isActive,
		}
		return db.DB.Create(&providerConfig).Error
	}

	// Update existing
	providerConfig.ProviderID = providerID
	providerConfig.InstanceName = instanceName
	providerConfig.ConfigJSON = string(configJSON)
	providerConfig.IsActive = isActive
	providerConfig.UpdatedAt = time.Now()
	return db.DB.Save(&providerConfig).Error
}

// LoadProviderConfigs loads all provider configurations from the database.
func (pm *ProviderManager) LoadProviderConfigs() ([]models.ProviderConfiguration, error) {
	if db.DB == nil {
		fmt.Printf("ProviderManager.LoadProviderConfigs: ERROR - database not initialized\n")
		return nil, fmt.Errorf("database not initialized")
	}

	var configs []models.ProviderConfiguration
	if err := db.DB.Find(&configs).Error; err != nil {
		fmt.Printf("ProviderManager.LoadProviderConfigs: ERROR - failed to load provider configs: %v\n", err)
		return nil, fmt.Errorf("failed to load provider configs: %w", err)
	}

	fmt.Printf("ProviderManager.LoadProviderConfigs: loaded %d provider configs from database\n", len(configs))
	for i, config := range configs {
		fmt.Printf("ProviderManager.LoadProviderConfigs: config[%d]: ProviderID=%s, InstanceID=%s, InstanceName=%s, IsActive=%v\n",
			i, config.ProviderID, config.InstanceID, config.InstanceName, config.IsActive)
	}

	return configs, nil
}

// RestoreProvider restores a provider instance from database configuration.
func (pm *ProviderManager) RestoreProvider(config models.ProviderConfiguration) (Provider, error) {
	// Handle migration: if InstanceID is empty, generate one from ProviderID
	instanceID := config.InstanceID
	if instanceID == "" {
		// Migration: create instanceID from ProviderID for old configurations
		instanceID = fmt.Sprintf("%s-1", config.ProviderID)
		// Update database with new instanceID
		if db.DB != nil {
			db.DB.Model(&config).Update("instance_id", instanceID)
			if config.InstanceName == "" {
				db.DB.Model(&config).Update("instance_name", config.ProviderID)
			}
		}
	}

	fmt.Printf("ProviderManager.RestoreProvider: restoring provider %s instance %s (IsActive: %v)\n", config.ProviderID, instanceID, config.IsActive)
	pm.mu.Lock()
	defer pm.mu.Unlock()

	factory, ok := pm.factories[config.ProviderID]
	if !ok {
		fmt.Printf("ProviderManager.RestoreProvider: ERROR - provider factory not found: %s\n", config.ProviderID)
		return nil, fmt.Errorf("provider not found: %s", config.ProviderID)
	}
	fmt.Printf("ProviderManager.RestoreProvider: factory found for %s\n", config.ProviderID)

	// Parse config JSON
	var providerConfig ProviderConfig
	if err := json.Unmarshal([]byte(config.ConfigJSON), &providerConfig); err != nil {
		fmt.Printf("ProviderManager.RestoreProvider: ERROR - failed to unmarshal config: %v\n", err)
		return nil, fmt.Errorf("failed to unmarshal config: %w", err)
	}
	fmt.Printf("ProviderManager.RestoreProvider: config unmarshaled successfully\n")

	// Add instanceID to config so provider can use it for isolated storage
	if providerConfig == nil {
		providerConfig = make(ProviderConfig)
	}
	providerConfig["_instance_id"] = instanceID

	// Create provider instance
	fmt.Printf("ProviderManager.RestoreProvider: creating provider instance\n")
	provider := factory()
	fmt.Printf("ProviderManager.RestoreProvider: provider instance created, calling Init\n")
	if err := provider.Init(providerConfig); err != nil {
		fmt.Printf("ProviderManager.RestoreProvider: ERROR - failed to initialize provider: %v\n", err)
		return nil, fmt.Errorf("failed to initialize provider: %w", err)
	}
	fmt.Printf("ProviderManager.RestoreProvider: provider initialized successfully\n")

	fmt.Printf("ProviderManager.RestoreProvider: adding provider to pm.providers map with instanceID %s\n", instanceID)
	pm.providers[instanceID] = provider
	fmt.Printf("ProviderManager.RestoreProvider: provider added to pm.providers (now %d providers: %v)\n",
		len(pm.providers), getMapKeys(pm.providers))

	// Set as active if needed
	if config.IsActive {
		fmt.Printf("ProviderManager.RestoreProvider: setting %s as active provider instance\n", instanceID)
		pm.activeInstanceID = instanceID
		fmt.Printf("ProviderManager.RestoreProvider: set %s as active provider instance\n", instanceID)
	}

	fmt.Printf("ProviderManager.RestoreProvider: successfully restored provider %s instance %s, returning\n", config.ProviderID, instanceID)
	return provider, nil
}
