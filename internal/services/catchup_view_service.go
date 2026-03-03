package services

import (
	"context"
	"errors"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrCatchUpLessonNotFound = errors.New("catch-up lesson not found")
	ErrUnauthorizedAccess    = errors.New("unauthorized access")
)

type CatchUpViewService struct {
	coursesCollection          *mongo.Collection
	studentsCollection         *mongo.Collection
	catchUpLessonsCollection   *mongo.Collection
	extractedContentCollection *mongo.Collection
	contentItemsCollection     *mongo.Collection
}

func NewCatchUpViewService(client *mongo.Client, dbName string) *CatchUpViewService {
	db := client.Database(dbName)

	return &CatchUpViewService{
		coursesCollection:          db.Collection("courses"),
		studentsCollection:         db.Collection("students"),
		catchUpLessonsCollection:   db.Collection("catchup_lessons"),
		extractedContentCollection: db.Collection("extracted_content"),
		contentItemsCollection:     db.Collection("content_items"),
	}
}

type CatchUpLessonReviewResponse struct {
	LessonID     string                `json:"lesson_id"`
	StudentID    string                `json:"student_id"`
	StudentName  string                `json:"student_name"`
	CourseID     string                `json:"course_id"`
	CourseName   string                `json:"course_name"`
	Status       string                `json:"status"`
	Explanation  string                `json:"explanation"`
	Quiz         []models.QuizQuestion `json:"quiz"`
	ContentAudit ContentAudit          `json:"content_audit"`
	WordCount    int                   `json:"word_count"`
	Warnings     []string              `json:"warnings,omitempty"`
	GeneratedAt  *time.Time            `json:"generated_at,omitempty"`
	DeliveredAt  *time.Time            `json:"delivered_at,omitempty"`
	CreatedAt    time.Time             `json:"created_at"`
}

type ContentAudit struct {
	Included []string `json:"included"`
	Excluded []string `json:"excluded"`
}

func (s *CatchUpViewService) GetCatchUpLessonForReview(
	ctx context.Context,
	courseID, studentID, userID, schoolID string,
) (*CatchUpLessonReviewResponse, error) {

	courseOID, err := bson.ObjectIDFromHex(courseID)
	if err != nil {
		return nil, errors.New("invalid course id")
	}

	studentOID, err := bson.ObjectIDFromHex(studentID)
	if err != nil {
		return nil, errors.New("invalid student id")
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, errors.New("invalid school id")
	}

	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        courseOID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&course)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrUnauthorizedAccess
		}
		return nil, err
	}

	var student models.Student
	err = s.studentsCollection.FindOne(ctx, bson.M{
		"_id":       studentOID,
		"school_id": schoolOID,
	}).Decode(&student)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("student not found")
		}
		return nil, err
	}

	var lesson models.CatchUpLesson
	err = s.catchUpLessonsCollection.FindOne(ctx, bson.M{
		"school_id":  schoolOID,
		"course_id":  courseOID,
		"student_id": studentOID,
	}).Decode(&lesson)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCatchUpLessonNotFound
		}
		return nil, err
	}

	var extractedContent models.ExtractedContent
	err = s.extractedContentCollection.FindOne(ctx, bson.M{
		"_id": lesson.ExtractedContentID,
	}).Decode(&extractedContent)
	if err != nil && !errors.Is(err, mongo.ErrNoDocuments) {
		return nil, err
	}

	contentAudit := s.buildContentAudit(ctx, extractedContent)

	response := &CatchUpLessonReviewResponse{
		LessonID:     lesson.ID.Hex(),
		StudentID:    student.ID.Hex(),
		StudentName:  student.Name,
		CourseID:     course.ID.Hex(),
		CourseName:   course.Name,
		Status:       string(lesson.Status),
		Explanation:  lesson.Explanation,
		Quiz:         lesson.Quiz,
		ContentAudit: contentAudit,
		WordCount:    extractedContent.WordCount,
		Warnings:     extractedContent.Warnings,
		GeneratedAt:  lesson.GeneratedAt,
		DeliveredAt:  lesson.DeliveredAt,
		CreatedAt:    lesson.CreatedAt,
	}

	return response, nil
}

func (s *CatchUpViewService) buildContentAudit(ctx context.Context, extractedContent models.ExtractedContent) ContentAudit {
	included := []string{}
	excluded := []string{}

	if len(extractedContent.ContentItemIDs) == 0 {
		return ContentAudit{
			Included: included,
			Excluded: excluded,
		}
	}

	cursor, err := s.contentItemsCollection.Find(ctx, bson.M{
		"_id": bson.M{"$in": extractedContent.ContentItemIDs},
	})
	if err != nil {
		return ContentAudit{Included: included, Excluded: excluded}
	}
	defer cursor.Close(ctx)

	var contentItems []models.ContentItem
	if err := cursor.All(ctx, &contentItems); err != nil {
		return ContentAudit{Included: included, Excluded: excluded}
	}

	for _, item := range contentItems {
		if item.Included {
			included = append(included, item.Title)
		} else {
			reason := "No supported attachments"
			if len(item.ExcludedNotes) > 0 {
				reason = item.ExcludedNotes[0]
			}
			excluded = append(excluded, item.Title+" ("+reason+")")
		}
	}

	return ContentAudit{
		Included: included,
		Excluded: excluded,
	}
}

func (s *CatchUpViewService) DeliverCatchUpLesson(
	ctx context.Context,
	lessonID, userID, schoolID string,
) error {

	lessonOID, err := bson.ObjectIDFromHex(lessonID)
	if err != nil {
		return errors.New("invalid lesson id")
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return errors.New("invalid school id")
	}

	var lesson models.CatchUpLesson
	err = s.catchUpLessonsCollection.FindOne(ctx, bson.M{
		"_id":       lessonOID,
		"school_id": schoolOID,
	}).Decode(&lesson)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrCatchUpLessonNotFound
		}
		return err
	}

	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        lesson.CourseID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&course)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return ErrUnauthorizedAccess
		}
		return err
	}

	now := time.Now().UTC()
	_, err = s.catchUpLessonsCollection.UpdateOne(ctx,
		bson.M{"_id": lessonOID},
		bson.M{"$set": bson.M{
			"status":       models.CatchUpStatusDelivered,
			"delivered_at": now,
			"updated_at":   now,
		}},
	)

	return err
}
