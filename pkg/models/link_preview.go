package models

import "time"

// LinkPreviewCache is application-wide URL data, independent of providers.
// Expiration is measured from generation, never refreshed by a cache read.
type LinkPreviewCache struct {
	URL       string `gorm:"primaryKey"`
	Payload   string
	ExpiresAt time.Time `gorm:"index"`
}
