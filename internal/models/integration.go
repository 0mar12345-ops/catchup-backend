package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type IntegrationStatus string

const (
	IntegrationConnected    IntegrationStatus = "connected"
	IntegrationDisconnected IntegrationStatus = "disconnected"
	IntegrationError        IntegrationStatus = "error"
)

type IntegrationConnection struct {
	ID                 bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID           bson.ObjectID       `bson:"school_id" json:"school_id"`
	Provider           IntegrationProvider `bson:"provider" json:"provider"`
	Status             IntegrationStatus   `bson:"status" json:"status"`
	ExternalTenantID   string              `bson:"external_tenant_id,omitempty" json:"external_tenant_id,omitempty"`
	CredentialsRef     string              `bson:"credentials_ref,omitempty" json:"credentials_ref,omitempty"`
	LastSuccessfulSync *time.Time          `bson:"last_successful_sync,omitempty" json:"last_successful_sync,omitempty"`
	LastSyncError      string              `bson:"last_sync_error,omitempty" json:"last_sync_error,omitempty"`
	SyncCursor         map[string]string   `bson:"sync_cursor,omitempty" json:"sync_cursor,omitempty"`
	CreatedAt          time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt          time.Time           `bson:"updated_at" json:"updated_at"`
}

type MappingEntityType string

const (
	MappingEntitySchool     MappingEntityType = "school"
	MappingEntityUser       MappingEntityType = "user"
	MappingEntityStudent    MappingEntityType = "student"
	MappingEntityCourse     MappingEntityType = "course"
	MappingEntityEnrollment MappingEntityType = "enrollment"
)

type ExternalEntityMapping struct {
	ID               bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID         bson.ObjectID       `bson:"school_id" json:"school_id"`
	Provider         IntegrationProvider `bson:"provider" json:"provider"`
	EntityType       MappingEntityType   `bson:"entity_type" json:"entity_type"`
	LocalID          bson.ObjectID       `bson:"local_id" json:"local_id"`
	ExternalID       string              `bson:"external_id" json:"external_id"`
	ExternalTenantID string              `bson:"external_tenant_id,omitempty" json:"external_tenant_id,omitempty"`
	LastSyncedAt     *time.Time          `bson:"last_synced_at,omitempty" json:"last_synced_at,omitempty"`
	CreatedAt        time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt        time.Time           `bson:"updated_at" json:"updated_at"`
}
