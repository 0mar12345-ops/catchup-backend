package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type BehaviourType string

const (
	BehaviourTypePositive BehaviourType = "positive"
	BehaviourTypeNegative BehaviourType = "negative"
)

type BehaviourLog struct {
	ID           bson.ObjectID `bson:"_id,omitempty" json:"id"`
	SchoolID     bson.ObjectID `bson:"school_id" json:"school_id"`
	TeacherID    bson.ObjectID `bson:"teacher_id" json:"teacher_id"`
	CourseID     bson.ObjectID `bson:"course_id" json:"course_id"`
	CourseName   string        `bson:"course_name" json:"course_name"`
	StudentEmail string        `bson:"student_email" json:"student_email"`
	StudentName  string        `bson:"student_name" json:"student_name"`
	Type         BehaviourType `bson:"type" json:"type"`
	Category     string        `bson:"category" json:"category"`
	Notes        string        `bson:"notes,omitempty" json:"notes,omitempty"`
	Date         time.Time     `bson:"date" json:"date"`
	CreatedAt    time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt    time.Time     `bson:"updated_at" json:"updated_at"`
}
