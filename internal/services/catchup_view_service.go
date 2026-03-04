package services

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	"github.com/jung-kurt/gofpdf"
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
	oauthCollection            *mongo.Collection
	userOAuthService           *UserOAuthService
	config                     *config.Config
}

type StudentCatchUpLessonResponse struct {
	ID                      string    `json:"id"`
	StudentID               string    `json:"student_id"`
	CourseID                string    `json:"course_id"`
	AbsenceDate             time.Time `json:"absence_date"`
	Status                  string    `json:"status"`
	Title                   string    `json:"title"`
	WordCount               int       `json:"word_count,omitempty"`
	CreatedAt               time.Time `json:"created_at"`
	UpdatedAt               time.Time `json:"updated_at"`
	HasDuplicateDate        bool      `json:"has_duplicate_date"`
	AlreadyDeliveredForDate bool      `json:"already_delivered_for_date"`
}

func NewCatchUpViewService(client *mongo.Client, dbName string, cfg *config.Config, userOAuthService *UserOAuthService) *CatchUpViewService {
	db := client.Database(dbName)

	return &CatchUpViewService{
		coursesCollection:          db.Collection("courses"),
		studentsCollection:         db.Collection("students"),
		catchUpLessonsCollection:   db.Collection("catchup_lessons"),
		extractedContentCollection: db.Collection("extracted_content"),
		contentItemsCollection:     db.Collection("content_items"),
		oauthCollection:            db.Collection("oauth_credentials"),
		userOAuthService:           userOAuthService,
		config:                     cfg,
	}
}

type CatchUpLessonReviewResponse struct {
	LessonID                string                `json:"lesson_id"`
	StudentID               string                `json:"student_id"`
	StudentName             string                `json:"student_name"`
	CourseID                string                `json:"course_id"`
	CourseName              string                `json:"course_name"`
	Status                  string                `json:"status"`
	Title                   string                `json:"title"`
	Explanation             string                `json:"explanation"`
	Quiz                    []models.QuizQuestion `json:"quiz"`
	ContentAudit            ContentAudit          `json:"content_audit"`
	WordCount               int                   `json:"word_count"`
	Warnings                []string              `json:"warnings,omitempty"`
	GeneratedAt             *time.Time            `json:"generated_at,omitempty"`
	DeliveredAt             *time.Time            `json:"delivered_at,omitempty"`
	CreatedAt               time.Time             `json:"created_at"`
	AlreadyDeliveredForDate bool                  `json:"already_delivered_for_date"`
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
		Title:        lesson.Title,
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

func (s *CatchUpViewService) GetCatchUpLessonByID(
	ctx context.Context,
	lessonID, userID, schoolID string,
) (*CatchUpLessonReviewResponse, error) {

	lessonOID, err := bson.ObjectIDFromHex(lessonID)
	if err != nil {
		return nil, errors.New("invalid lesson id")
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, errors.New("invalid school id")
	}

	var lesson models.CatchUpLesson
	err = s.catchUpLessonsCollection.FindOne(ctx, bson.M{
		"_id":       lessonOID,
		"school_id": schoolOID,
	}).Decode(&lesson)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCatchUpLessonNotFound
		}
		return nil, err
	}

	// Verify the teacher has access to this course
	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        lesson.CourseID,
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
		"_id":       lesson.StudentID,
		"school_id": schoolOID,
	}).Decode(&student)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("student not found")
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

	// Check if any other lesson for the same absence date has been delivered
	var absenceRecord models.AbsenceRecord
	err = s.catchUpLessonsCollection.Database().Collection("absence_records").FindOne(ctx, bson.M{
		"_id": lesson.AbsenceRecordID,
	}).Decode(&absenceRecord)

	alreadyDeliveredForDate := false
	if err == nil {
		absenceDate := absenceRecord.AbsentOn.Truncate(24 * time.Hour)

		// Find all lessons for this student on the same absence date
		var allLessonsForDate []models.CatchUpLesson
		cursor, err := s.catchUpLessonsCollection.Find(ctx, bson.M{
			"school_id":  schoolOID,
			"course_id":  lesson.CourseID,
			"student_id": lesson.StudentID,
		})
		if err == nil {
			defer cursor.Close(ctx)
			cursor.All(ctx, &allLessonsForDate)

			for _, otherLesson := range allLessonsForDate {
				// Skip the current lesson
				if otherLesson.ID == lesson.ID {
					continue
				}

				// Check if this other lesson is for the same date and has been delivered
				var otherAbsenceRecord models.AbsenceRecord
				err := s.catchUpLessonsCollection.Database().Collection("absence_records").FindOne(ctx, bson.M{
					"_id": otherLesson.AbsenceRecordID,
				}).Decode(&otherAbsenceRecord)

				if err == nil {
					otherDate := otherAbsenceRecord.AbsentOn.Truncate(24 * time.Hour)
					if otherDate.Equal(absenceDate) && otherLesson.Status == models.CatchUpStatusDelivered {
						alreadyDeliveredForDate = true
						break
					}
				}
			}
		}
	}

	contentAudit := s.buildContentAudit(ctx, extractedContent)

	response := &CatchUpLessonReviewResponse{
		LessonID:                lesson.ID.Hex(),
		StudentID:               student.ID.Hex(),
		StudentName:             student.Name,
		CourseID:                course.ID.Hex(),
		CourseName:              course.Name,
		Status:                  string(lesson.Status),
		Title:                   lesson.Title,
		Explanation:             lesson.Explanation,
		Quiz:                    lesson.Quiz,
		ContentAudit:            contentAudit,
		WordCount:               extractedContent.WordCount,
		Warnings:                extractedContent.Warnings,
		GeneratedAt:             lesson.GeneratedAt,
		DeliveredAt:             lesson.DeliveredAt,
		CreatedAt:               lesson.CreatedAt,
		AlreadyDeliveredForDate: alreadyDeliveredForDate,
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
	dueDate *time.Time,
	title *string,
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

	// Get student information
	var student models.Student
	err = s.studentsCollection.FindOne(ctx, bson.M{
		"_id":       lesson.StudentID,
		"school_id": schoolOID,
	}).Decode(&student)
	if err != nil {
		return fmt.Errorf("failed to find student: %w", err)
	}

	// Get OAuth credentials for teacher
	var oauthCred models.OAuthCredential
	err = s.oauthCollection.FindOne(ctx, bson.M{
		"school_id": schoolOID,
		"user_id":   userOID,
	}).Decode(&oauthCred)
	if err != nil {
		return errors.New("oauth credentials not found - teacher needs to re-authorize")
	}

	// Get absence record for date
	var absenceRecord models.AbsenceRecord
	err = s.catchUpLessonsCollection.Database().Collection("absence_records").FindOne(ctx, bson.M{
		"_id": lesson.AbsenceRecordID,
	}).Decode(&absenceRecord)
	if err != nil {
		return fmt.Errorf("failed to find absence record: %w", err)
	}

	// Update title if provided (before creating assignment so it's used in PDF and assignment)
	if title != nil && *title != "" {
		lesson.Title = *title
	}

	// Create Google Classroom assignment
	assignmentID, assignmentLink, err := s.createClassroomAssignment(ctx, &oauthCred, &course, &student, &lesson, absenceRecord.AbsentOn, dueDate)
	if err != nil {
		return fmt.Errorf("failed to create classroom assignment: %w", err)
	}

	// Update lesson with delivery information
	now := time.Now().UTC()
	updateFields := bson.M{
		"status":                    models.CatchUpStatusDelivered,
		"delivered_at":              now,
		"updated_at":                now,
		"classroom_assignment_id":   assignmentID,
		"classroom_assignment_link": assignmentLink,
	}

	// Include title in database update if it was provided
	if title != nil && *title != "" {
		updateFields["title"] = *title
	}

	_, err = s.catchUpLessonsCollection.UpdateOne(ctx,
		bson.M{"_id": lessonOID},
		bson.M{"$set": updateFields},
	)

	return err
}

func (s *CatchUpViewService) GetStudentCatchUpLessons(
	ctx context.Context,
	courseID, studentID, userID, schoolID string,
) ([]StudentCatchUpLessonResponse, error) {
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

	cursor, err := s.catchUpLessonsCollection.Find(ctx, bson.M{
		"school_id":  schoolOID,
		"course_id":  courseOID,
		"student_id": studentOID,
	})
	if err != nil {
		return nil, err
	}
	defer cursor.Close(ctx)

	var lessons []models.CatchUpLesson
	if err = cursor.All(ctx, &lessons); err != nil {
		return nil, err
	}

	// Build a map of absence dates to detect duplicates and track delivered status
	absenceDateMap := make(map[time.Time][]models.CatchUpLesson)
	absenceDateDelivered := make(map[time.Time]bool)

	for _, lesson := range lessons {
		var absenceRecord models.AbsenceRecord
		err := s.catchUpLessonsCollection.Database().Collection("absence_records").FindOne(ctx, bson.M{
			"_id": lesson.AbsenceRecordID,
		}).Decode(&absenceRecord)
		if err == nil {
			absenceDate := absenceRecord.AbsentOn.Truncate(24 * time.Hour)
			absenceDateMap[absenceDate] = append(absenceDateMap[absenceDate], lesson)
			if lesson.Status == models.CatchUpStatusDelivered {
				absenceDateDelivered[absenceDate] = true
			}
		}
	}

	response := make([]StudentCatchUpLessonResponse, 0, len(lessons))
	for _, lesson := range lessons {
		lessonResp := StudentCatchUpLessonResponse{
			ID:        lesson.ID.Hex(),
			StudentID: lesson.StudentID.Hex(),
			CourseID:  lesson.CourseID.Hex(),
			Status:    string(lesson.Status),
			Title:     lesson.Title,
			CreatedAt: lesson.CreatedAt,
			UpdatedAt: lesson.UpdatedAt,
		}

		var absenceRecord models.AbsenceRecord
		err := s.catchUpLessonsCollection.Database().Collection("absence_records").FindOne(ctx, bson.M{
			"_id": lesson.AbsenceRecordID,
		}).Decode(&absenceRecord)
		if err == nil {
			lessonResp.AbsenceDate = absenceRecord.AbsentOn
			absenceDate := absenceRecord.AbsentOn.Truncate(24 * time.Hour)

			// Check if there are multiple lessons for this date
			if len(absenceDateMap[absenceDate]) > 1 {
				lessonResp.HasDuplicateDate = true
			}

			// Check if any lesson for this date has been delivered
			if absenceDateDelivered[absenceDate] {
				lessonResp.AlreadyDeliveredForDate = true
			}
		}

		var extractedContent models.ExtractedContent
		err = s.extractedContentCollection.FindOne(ctx, bson.M{
			"_id": lesson.ExtractedContentID,
		}).Decode(&extractedContent)
		if err == nil {
			lessonResp.WordCount = extractedContent.WordCount
		}

		response = append(response, lessonResp)
	}

	return response, nil
}

func (s *CatchUpViewService) createOAuthClient(ctx context.Context, oauthCred *models.OAuthCredential) (*http.Client, error) {
	if oauthCred.RefreshTokenEnc == "" {
		return nil, errors.New("refresh token not found - user needs to re-authorize")
	}

	// Use centralized refresh method from UserOAuthService
	return s.userOAuthService.RefreshOAuthToken(ctx, oauthCred)
}

func (s *CatchUpViewService) createClassroomAssignment(
	ctx context.Context,
	oauthCred *models.OAuthCredential,
	course *models.Course,
	student *models.Student,
	lesson *models.CatchUpLesson,
	absenceDate time.Time,
	dueDate *time.Time,
) (string, string, error) {
	// Get external course ID from Google Classroom
	var externalCourseID string
	for _, ref := range course.ExternalRefs {
		if ref.Provider == models.ProviderGoogleClassroom {
			externalCourseID = ref.ExternalID
			break
		}
	}
	if externalCourseID == "" {
		return "", "", errors.New("course not synced from Google Classroom")
	}

	// Get external student ID
	var externalStudentID string
	for _, ref := range student.ExternalRefs {
		if ref.Provider == models.ProviderGoogleClassroom {
			externalStudentID = ref.ExternalID
			break
		}
	}
	if externalStudentID == "" {
		return "", "", errors.New("student not synced from Google Classroom")
	}

	client, err := s.createOAuthClient(ctx, oauthCred)
	if err != nil {
		return "", "", err
	}

	// Generate PDF content
	pdfBytes, err := s.generatePDF(lesson, absenceDate, student)
	if err != nil {
		return "", "", fmt.Errorf("failed to generate PDF: %w", err)
	}

	// Upload PDF to Google Drive
	driveFileID, err := s.uploadPDFToDrive(ctx, client, pdfBytes, fmt.Sprintf("Catch-Up_%s_%s.pdf", student.Name, absenceDate.Format("2006-01-02")))
	if err != nil {
		return "", "", fmt.Errorf("failed to upload PDF to Drive: %w", err)
	}

	// Create the assignment payload with PDF attachment
	assignmentTitle := lesson.Title
	if assignmentTitle == "" {
		assignmentTitle = fmt.Sprintf("Catch-Up: Content from %s", absenceDate.Format("Jan 2"))
	}
	assignmentPayload := map[string]interface{}{
		"title":        assignmentTitle,
		"description":  fmt.Sprintf("Please review the attached PDF for the catch-up lesson covering material from %s.", absenceDate.Format("Monday, January 2, 2006")),
		"workType":     "ASSIGNMENT",
		"state":        "PUBLISHED",
		"maxPoints":    100,
		"assigneeMode": "INDIVIDUAL_STUDENTS",
		"individualStudentsOptions": map[string]interface{}{
			"studentIds": []string{externalStudentID},
		},
		"materials": []map[string]interface{}{
			{
				"driveFile": map[string]interface{}{
					"driveFile": map[string]interface{}{
						"id":    driveFileID,
						"title": fmt.Sprintf("Catch-Up_%s_%s.pdf", student.Name, absenceDate.Format("2006-01-02")),
					},
				},
			},
		},
	}

	// Add due date if provided
	if dueDate != nil {
		assignmentPayload["dueDate"] = map[string]interface{}{
			"year":  dueDate.Year(),
			"month": int(dueDate.Month()),
			"day":   dueDate.Day(),
		}
		assignmentPayload["dueTime"] = map[string]interface{}{
			"hours":   23,
			"minutes": 59,
		}
	}

	payloadBytes, err := json.Marshal(assignmentPayload)
	if err != nil {
		return "", "", fmt.Errorf("failed to marshal assignment payload: %w", err)
	}

	// Create the assignment via Classroom API
	url := fmt.Sprintf("https://classroom.googleapis.com/v1/courses/%s/courseWork", externalCourseID)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(payloadBytes))
	if err != nil {
		return "", "", err
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		return "", "", fmt.Errorf("failed to create classroom assignment: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		body, _ := io.ReadAll(resp.Body)
		return "", "", fmt.Errorf("classroom API error: %d - %s", resp.StatusCode, string(body))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", "", fmt.Errorf("failed to decode classroom response: %w", err)
	}

	assignmentID, ok := result["id"].(string)
	if !ok {
		return "", "", errors.New("failed to get assignment ID from response")
	}

	// Get the assignment link
	assignmentLink, _ := result["alternateLink"].(string)

	return assignmentID, assignmentLink, nil
}

func (s *CatchUpViewService) generatePDF(lesson *models.CatchUpLesson, absenceDate time.Time, student *models.Student) ([]byte, error) {
	pdf := gofpdf.New("P", "mm", "A4", "")
	pdf.AddPage()

	// Title
	pdf.SetFont("Arial", "B", 18)
	title := lesson.Title
	if title == "" {
		title = fmt.Sprintf("Catch-Up Lesson for %s", absenceDate.Format("January 2, 2006"))
	}
	pdf.CellFormat(0, 10, title, "", 1, "C", false, 0, "")
	pdf.Ln(5)

	// Student name
	pdf.SetFont("Arial", "I", 12)
	pdf.CellFormat(0, 6, fmt.Sprintf("Student: %s", student.Name), "", 1, "L", false, 0, "")
	pdf.Ln(5)

	// Learning Objectives
	if len(lesson.LearningObjectives) > 0 {
		pdf.SetFont("Arial", "B", 14)
		pdf.CellFormat(0, 8, "Learning Objectives", "", 1, "L", false, 0, "")
		pdf.SetFont("Arial", "", 11)
		for i, obj := range lesson.LearningObjectives {
			pdf.MultiCell(0, 5, fmt.Sprintf("%d. %s", i+1, obj), "", "L", false)
		}
		pdf.Ln(5)
	}

	// Explanation - strip HTML tags and format
	pdf.SetFont("Arial", "B", 14)
	pdf.CellFormat(0, 8, "Lesson Content", "", 1, "L", false, 0, "")
	pdf.SetFont("Arial", "", 11)

	// Simple HTML tag removal
	cleanText := stripHTMLTags(lesson.Explanation)
	pdf.MultiCell(0, 5, cleanText, "", "L", false)
	pdf.Ln(5)

	// Quiz Questions
	if len(lesson.Quiz) > 0 {
		pdf.AddPage()
		pdf.SetFont("Arial", "B", 14)
		pdf.CellFormat(0, 8, "Quiz Questions", "", 1, "L", false, 0, "")
		pdf.Ln(3)

		for i, q := range lesson.Quiz {
			pdf.SetFont("Arial", "B", 11)
			pdf.MultiCell(0, 5, fmt.Sprintf("Question %d: %s", i+1, q.Question), "", "L", false)

			if q.Type == models.QuizQuestionMCQ && len(q.Options) > 0 {
				pdf.SetFont("Arial", "", 10)
				for j, opt := range q.Options {
					pdf.MultiCell(0, 4, fmt.Sprintf("   %c) %s", 'A'+j, opt), "", "L", false)
				}
			}
			pdf.Ln(4)
		}
	}

	var buf bytes.Buffer
	err := pdf.Output(&buf)
	if err != nil {
		return nil, err
	}

	return buf.Bytes(), nil
}

func stripHTMLTags(html string) string {
	// Simple HTML tag stripper - replace common tags with formatting
	text := html
	text = strings.ReplaceAll(text, "<br>", "\n")
	text = strings.ReplaceAll(text, "<br/>", "\n")
	text = strings.ReplaceAll(text, "<br />", "\n")
	text = strings.ReplaceAll(text, "</p>", "\n\n")
	text = strings.ReplaceAll(text, "</li>", "\n")
	text = strings.ReplaceAll(text, "</h2>", "\n\n")
	text = strings.ReplaceAll(text, "</h3>", "\n\n")
	text = strings.ReplaceAll(text, "</h4>", "\n")

	// Remove all remaining HTML tags
	for strings.Contains(text, "<") && strings.Contains(text, ">") {
		start := strings.Index(text, "<")
		end := strings.Index(text, ">")
		if start < end {
			text = text[:start] + text[end+1:]
		} else {
			break
		}
	}

	return strings.TrimSpace(text)
}

func (s *CatchUpViewService) uploadPDFToDrive(ctx context.Context, client *http.Client, pdfBytes []byte, filename string) (string, error) {
	// Create multipart form data
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Add metadata
	metadata := map[string]interface{}{
		"name":     filename,
		"mimeType": "application/pdf",
	}
	metadataBytes, _ := json.Marshal(metadata)

	metadataPart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"application/json; charset=UTF-8"},
	})
	if err != nil {
		return "", err
	}
	metadataPart.Write(metadataBytes)

	// Add file content
	filePart, err := writer.CreatePart(map[string][]string{
		"Content-Type": {"application/pdf"},
	})
	if err != nil {
		return "", err
	}
	filePart.Write(pdfBytes)

	err = writer.Close()
	if err != nil {
		return "", err
	}

	// Upload to Drive
	req, err := http.NewRequestWithContext(ctx, "POST",
		"https://www.googleapis.com/upload/drive/v3/files?uploadType=multipart",
		body)
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusCreated {
		bodyBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("drive upload failed: %d - %s", resp.StatusCode, string(bodyBytes))
	}

	var result map[string]interface{}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", err
	}

	fileID, ok := result["id"].(string)
	if !ok {
		return "", errors.New("failed to get file ID from Drive response")
	}

	return fileID, nil
}
