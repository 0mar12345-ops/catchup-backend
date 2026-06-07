package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TermOverviewEntry maps a week number to topic and assessment status.
type TermOverviewEntry struct {
	WeekNumber   int    `bson:"week_number" json:"week_number"`
	TopicTaught  string `bson:"topic_taught" json:"topic_taught"`
	Assessment   string `bson:"assessment" json:"assessment"`
	AssessmentYN bool   `bson:"assessment_yes_no" json:"assessment_yes_no"`
}

// TermOverviewUpload stores an uploaded term overview file and its parsed entries.
type TermOverviewUpload struct {
	ID           bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID       `bson:"school_id" json:"school_id"`
	TeacherID    bson.ObjectID       `bson:"teacher_id" json:"teacher_id"`
	FileName     string              `bson:"file_name" json:"file_name"`
	FileType     string              `bson:"file_type" json:"file_type"`
	TermLabel    string              `bson:"term_label,omitempty" json:"term_label,omitempty"`
	Entries      []TermOverviewEntry `bson:"entries" json:"entries"`
	UploadedAt   time.Time           `bson:"uploaded_at" json:"uploaded_at"`
	CreatedAt    time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time           `bson:"updated_at" json:"updated_at"`
}
