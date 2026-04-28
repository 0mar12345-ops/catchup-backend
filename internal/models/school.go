package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type School struct {
	ID           bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	Name         string              `bson:"name" json:"name"`
	Code         string              `bson:"code" json:"code"`
	Domain       string              `bson:"domain,omitempty" json:"domain,omitempty"`
	Timezone     string              `bson:"timezone,omitempty" json:"timezone,omitempty"`
	IsActive     bool                `bson:"is_active" json:"is_active"`
	ExternalRefs []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time           `bson:"updated_at" json:"updated_at"`
}
