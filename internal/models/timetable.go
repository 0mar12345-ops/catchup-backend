package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

// TimetableEntry represents one timetable slot.
type TimetableEntry struct {
	DayNumber    int    `bson:"day_number" json:"day_number"`
	PeriodNumber int    `bson:"period_number" json:"period_number"`
	ClassName    string `bson:"class_name" json:"class_name"`
	RoomNumber   string `bson:"room_number" json:"room_number"`
	Subject      string `bson:"subject" json:"subject"`
}

// TimetableUpload stores an uploaded timetable file and its parsed entries.
type TimetableUpload struct {
	ID           bson.ObjectID    `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID    `bson:"school_id" json:"school_id"`
	TeacherID    bson.ObjectID    `bson:"teacher_id" json:"teacher_id"`
	FileName     string           `bson:"file_name" json:"file_name"`
	FileType     string           `bson:"file_type" json:"file_type"`
	TermLabel    string           `bson:"term_label,omitempty" json:"term_label,omitempty"`
	Entries      []TimetableEntry `bson:"entries" json:"entries"`
	UploadedAt   time.Time        `bson:"uploaded_at" json:"uploaded_at"`
	CreatedAt    time.Time        `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time        `bson:"updated_at" json:"updated_at"`
}
