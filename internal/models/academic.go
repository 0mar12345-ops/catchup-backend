package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type CourseSource string

const (
	CourseSourceGoogleClassroom CourseSource = "google_classroom"
	CourseSourceISAMS           CourseSource = "isams"
	CourseSourceManual          CourseSource = "manual"
)

type Course struct {
	ID             bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID       bson.ObjectID       `bson:"school_id" json:"school_id"`
	TeacherID      bson.ObjectID       `bson:"teacher_id" json:"teacher_id"`
	Name           string              `bson:"name" json:"name"`
	Section        string              `bson:"section,omitempty" json:"section,omitempty"`
	Subject        string              `bson:"subject,omitempty" json:"subject,omitempty"`
	Room           string              `bson:"room,omitempty" json:"room,omitempty"`
	GradeLevel     string              `bson:"grade_level,omitempty" json:"grade_level,omitempty"`
	StudentCount   int                 `bson:"student_count" json:"student_count"`
	Source         CourseSource        `bson:"source" json:"source"`
	ExternalRefs   []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	IsArchived     bool                `bson:"is_archived" json:"is_archived"`
	LastSyncedAt   *time.Time          `bson:"last_synced_at,omitempty" json:"last_synced_at,omitempty"`
	MetadataCached *time.Time          `bson:"metadata_cached_at,omitempty" json:"metadata_cached_at,omitempty"`
	CreatedAt      time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time           `bson:"updated_at" json:"updated_at"`
}

type Student struct {
	ID           bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID       `bson:"school_id" json:"school_id"`
	Name         string              `bson:"name" json:"name"`
	Email        string              `bson:"email,omitempty" json:"email,omitempty"`
	GradeLevel   string              `bson:"grade_level,omitempty" json:"grade_level,omitempty"`
	IsActive     bool                `bson:"is_active" json:"is_active"`
	ExternalRefs []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time           `bson:"updated_at" json:"updated_at"`
}

type EnrollmentStatus string

const (
	EnrollmentActive   EnrollmentStatus = "active"
	EnrollmentInactive EnrollmentStatus = "inactive"
)

type Enrollment struct {
	ID           bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID       `bson:"school_id" json:"school_id"`
	CourseID     bson.ObjectID       `bson:"course_id" json:"course_id"`
	StudentID    bson.ObjectID       `bson:"student_id" json:"student_id"`
	Status       EnrollmentStatus    `bson:"status" json:"status"`
	ExternalRefs []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time           `bson:"updated_at" json:"updated_at"`
}
