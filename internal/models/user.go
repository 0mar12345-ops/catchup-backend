package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type User struct {
	ID           bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID       `bson:"school_id" json:"school_id"`
	Role         UserRole            `bson:"role" json:"role"`
	Name         string              `bson:"name" json:"name"`
	Email        string              `bson:"email" json:"email"`
	GoogleUserID string              `bson:"google_user_id,omitempty" json:"google_user_id,omitempty"`
	IsActive     bool                `bson:"is_active" json:"is_active"`
	LastLoginAt  *time.Time          `bson:"last_login_at,omitempty" json:"last_login_at,omitempty"`
	ExternalRefs []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time           `bson:"updated_at" json:"updated_at"`
}

type UserRole string

const (
	UserRoleTeacher UserRole = "teacher"
	UserRoleStudent UserRole = "student"
	UserRoleAdmin   UserRole = "admin"
)

type OAuthCredential struct {
	ID                bson.ObjectID `bson:"_id,omitempty" json:"id"`
	SchoolID          bson.ObjectID `bson:"school_id" json:"school_id"`
	UserID            bson.ObjectID `bson:"user_id" json:"user_id"`
	Provider          string        `bson:"provider" json:"provider"`
	Scopes            []string      `bson:"scopes" json:"scopes"`
	RefreshTokenEnc   string        `bson:"refresh_token_enc" json:"-"`
	AccessTokenEnc    string        `bson:"access_token_enc,omitempty" json:"-"`
	AccessTokenExpiry *time.Time    `bson:"access_token_expiry,omitempty" json:"access_token_expiry,omitempty"`
	GrantedAt         time.Time     `bson:"granted_at" json:"granted_at"`
	RevokedAt         *time.Time    `bson:"revoked_at,omitempty" json:"revoked_at,omitempty"`
	CreatedAt         time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt         time.Time     `bson:"updated_at" json:"updated_at"`
}
