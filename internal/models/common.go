package models

import "time"

type IntegrationProvider string

const (
	ProviderGoogleClassroom IntegrationProvider = "google_classroom"
	ProviderGoogleOAuth     IntegrationProvider = "google_oauth"
	ProviderISAMS           IntegrationProvider = "isams"
	ProviderManual          IntegrationProvider = "manual"
)

type ExternalSystemRef struct {
	Provider        IntegrationProvider `bson:"provider" json:"provider"`
	ExternalID      string              `bson:"external_id" json:"external_id"`
	ExternalCode    string              `bson:"external_code,omitempty" json:"external_code,omitempty"`
	ExternalTenant  string              `bson:"external_tenant,omitempty" json:"external_tenant,omitempty"`
	LastSyncedAt    *time.Time          `bson:"last_synced_at,omitempty" json:"last_synced_at,omitempty"`
	ExternalUpdated *time.Time          `bson:"external_updated_at,omitempty" json:"external_updated_at,omitempty"`
}
