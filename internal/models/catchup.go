package models

import (
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
)

type CatchUpStatus string

const (
	CatchUpStatusEmpty     CatchUpStatus = "empty"
	CatchUpStatusGenerated CatchUpStatus = "generated"
	CatchUpStatusDelivered CatchUpStatus = "delivered"
	CatchUpStatusCompleted CatchUpStatus = "completed"
	CatchUpStatusFailed    CatchUpStatus = "failed"
)

type AbsenceRecord struct {
	ID                bson.ObjectID `bson:"_id,omitempty" json:"id"`
	SchoolID          bson.ObjectID `bson:"school_id" json:"school_id"`
	CourseID          bson.ObjectID `bson:"course_id" json:"course_id"`
	StudentID         bson.ObjectID `bson:"student_id" json:"student_id"`
	AbsentOn          time.Time     `bson:"absent_on" json:"absent_on"`
	MarkedByTeacherID bson.ObjectID `bson:"marked_by_teacher_id" json:"marked_by_teacher_id"`
	Source            string        `bson:"source" json:"source"`
	IsLocked          bool          `bson:"is_locked" json:"is_locked"`
	CreatedAt         time.Time     `bson:"created_at" json:"created_at"`
	UpdatedAt         time.Time     `bson:"updated_at" json:"updated_at"`
}

type IngestionJobStatus string

const (
	IngestionJobPending   IngestionJobStatus = "pending"
	IngestionJobRunning   IngestionJobStatus = "running"
	IngestionJobCompleted IngestionJobStatus = "completed"
	IngestionJobFailed    IngestionJobStatus = "failed"
)

type IngestionJob struct {
	ID                bson.ObjectID      `bson:"_id,omitempty" json:"id"`
	SchoolID          bson.ObjectID      `bson:"school_id" json:"school_id"`
	CourseID          bson.ObjectID      `bson:"course_id" json:"course_id"`
	AbsenceRecordID   bson.ObjectID      `bson:"absence_record_id" json:"absence_record_id"`
	TriggeredByUserID bson.ObjectID      `bson:"triggered_by_user_id" json:"triggered_by_user_id"`
	Status            IngestionJobStatus `bson:"status" json:"status"`
	StartedAt         *time.Time         `bson:"started_at,omitempty" json:"started_at,omitempty"`
	CompletedAt       *time.Time         `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	FailureReason     string             `bson:"failure_reason,omitempty" json:"failure_reason,omitempty"`
	CreatedAt         time.Time          `bson:"created_at" json:"created_at"`
	UpdatedAt         time.Time          `bson:"updated_at" json:"updated_at"`
}

type ContentType string

const (
	ContentTypeAssignment   ContentType = "assignment"
	ContentTypeMaterial     ContentType = "material"
	ContentTypeAnnouncement ContentType = "announcement"
)

type AttachmentKind string

const (
	AttachmentKindGoogleDoc   AttachmentKind = "google_doc"
	AttachmentKindGoogleSlide AttachmentKind = "google_slide"
	AttachmentKindPDF         AttachmentKind = "pdf"
	AttachmentKindExternalURL AttachmentKind = "external_url"
	AttachmentKindVideo       AttachmentKind = "video"
	AttachmentKindImage       AttachmentKind = "image"
	AttachmentKindOther       AttachmentKind = "other"
)

type ContentAttachment struct {
	Title        string         `bson:"title,omitempty" json:"title,omitempty"`
	URL          string         `bson:"url,omitempty" json:"url,omitempty"`
	MimeType     string         `bson:"mime_type,omitempty" json:"mime_type,omitempty"`
	Kind         AttachmentKind `bson:"kind" json:"kind"`
	IsSupported  bool           `bson:"is_supported" json:"is_supported"`
	ExcludeCause string         `bson:"exclude_cause,omitempty" json:"exclude_cause,omitempty"`
	ExternalID   string         `bson:"external_id,omitempty" json:"external_id,omitempty"`
}

type ContentItem struct {
	ID             bson.ObjectID       `bson:"_id,omitempty" json:"id"`
	SchoolID       bson.ObjectID       `bson:"school_id" json:"school_id"`
	CourseID       bson.ObjectID       `bson:"course_id" json:"course_id"`
	IngestionJobID bson.ObjectID       `bson:"ingestion_job_id" json:"ingestion_job_id"`
	Type           ContentType         `bson:"type" json:"type"`
	Title          string              `bson:"title" json:"title"`
	Description    string              `bson:"description,omitempty" json:"description,omitempty"`
	Attachments    []ContentAttachment `bson:"attachments,omitempty" json:"attachments,omitempty"`
	Included       bool                `bson:"included" json:"included"`
	ExcludedNotes  []string            `bson:"excluded_notes,omitempty" json:"excluded_notes,omitempty"`
	ExternalRefs   []ExternalSystemRef `bson:"external_refs,omitempty" json:"external_refs,omitempty"`
	CreatedAt      time.Time           `bson:"created_at" json:"created_at"`
	UpdatedAt      time.Time           `bson:"updated_at" json:"updated_at"`
}

type ExtractedContent struct {
	ID                   bson.ObjectID   `bson:"_id,omitempty" json:"id"`
	SchoolID             bson.ObjectID   `bson:"school_id" json:"school_id"`
	CourseID             bson.ObjectID   `bson:"course_id" json:"course_id"`
	AbsenceRecordID      bson.ObjectID   `bson:"absence_record_id" json:"absence_record_id"`
	IngestionJobID       bson.ObjectID   `bson:"ingestion_job_id" json:"ingestion_job_id"`
	ContentItemIDs       []bson.ObjectID `bson:"content_item_ids" json:"content_item_ids"`
	CombinedText         string          `bson:"combined_text" json:"combined_text"`
	WordCount            int             `bson:"word_count" json:"word_count"`
	MinWordCountRequired int             `bson:"min_word_count_required" json:"min_word_count_required"`
	MeetsThreshold       bool            `bson:"meets_threshold" json:"meets_threshold"`
	Warnings             []string        `bson:"warnings,omitempty" json:"warnings,omitempty"`
	CreatedAt            time.Time       `bson:"created_at" json:"created_at"`
	UpdatedAt            time.Time       `bson:"updated_at" json:"updated_at"`
}

type QuizQuestionType string

const (
	QuizQuestionMCQ         QuizQuestionType = "mcq"
	QuizQuestionShortAnswer QuizQuestionType = "short_answer"
)

type QuizQuestion struct {
	Question string           `bson:"question" json:"question"`
	Type     QuizQuestionType `bson:"type" json:"type"`
	Options  []string         `bson:"options,omitempty" json:"options,omitempty"`
	Answer   string           `bson:"answer,omitempty" json:"answer,omitempty"`
}

type CatchUpLesson struct {
	ID                      bson.ObjectID  `bson:"_id,omitempty" json:"id"`
	SchoolID                bson.ObjectID  `bson:"school_id" json:"school_id"`
	CourseID                bson.ObjectID  `bson:"course_id" json:"course_id"`
	StudentID               bson.ObjectID  `bson:"student_id" json:"student_id"`
	AbsenceRecordID         bson.ObjectID  `bson:"absence_record_id" json:"absence_record_id"`
	ExtractedContentID      bson.ObjectID  `bson:"extracted_content_id" json:"extracted_content_id"`
	Status                  CatchUpStatus  `bson:"status" json:"status"`
	Explanation             string         `bson:"explanation" json:"explanation"`
	LearningObjectives      []string       `bson:"learning_objectives,omitempty" json:"learning_objectives,omitempty"`
	Quiz                    []QuizQuestion `bson:"quiz" json:"quiz"`
	GenerationModel         string         `bson:"generation_model,omitempty" json:"generation_model,omitempty"`
	PromptVersion           string         `bson:"prompt_version,omitempty" json:"prompt_version,omitempty"`
	RegenerationCount       int            `bson:"regeneration_count" json:"regeneration_count"`
	ClassroomAssignmentID   string         `bson:"classroom_assignment_id,omitempty" json:"classroom_assignment_id,omitempty"`
	ClassroomAssignmentLink string         `bson:"classroom_assignment_link,omitempty" json:"classroom_assignment_link,omitempty"`
	GeneratedAt             *time.Time     `bson:"generated_at,omitempty" json:"generated_at,omitempty"`
	DeliveredAt             *time.Time     `bson:"delivered_at,omitempty" json:"delivered_at,omitempty"`
	CompletedAt             *time.Time     `bson:"completed_at,omitempty" json:"completed_at,omitempty"`
	LockedAt                *time.Time     `bson:"locked_at,omitempty" json:"locked_at,omitempty"`
	CreatedAt               time.Time      `bson:"created_at" json:"created_at"`
	UpdatedAt               time.Time      `bson:"updated_at" json:"updated_at"`
}

type CatchUpEventType string

const (
	CatchUpEventGenerated   CatchUpEventType = "generated"
	CatchUpEventDelivered   CatchUpEventType = "delivered"
	CatchUpEventCompleted   CatchUpEventType = "completed"
	CatchUpEventRegenerated CatchUpEventType = "regenerated"
	CatchUpEventFailed      CatchUpEventType = "failed"
)

type CatchUpEvent struct {
	ID              bson.ObjectID    `bson:"_id,omitempty" json:"id"`
	SchoolID        bson.ObjectID    `bson:"school_id" json:"school_id"`
	CatchUpLessonID bson.ObjectID    `bson:"catchup_lesson_id" json:"catchup_lesson_id"`
	ActorUserID     *bson.ObjectID   `bson:"actor_user_id,omitempty" json:"actor_user_id,omitempty"`
	EventType       CatchUpEventType `bson:"event_type" json:"event_type"`
	Message         string           `bson:"message,omitempty" json:"message,omitempty"`
	CreatedAt       time.Time        `bson:"created_at" json:"created_at"`
}
