package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	"github.com/gen2brain/go-fitz"
	"github.com/ledongthuc/pdf"
	"github.com/otiai10/gosseract/v2"
	openai "github.com/sashabaranov/go-openai"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

var (
	ErrInvalidCatchUpCourseID  = errors.New("invalid course id")
	ErrInvalidCatchUpStudentID = errors.New("invalid student id")
	ErrNoContentFound          = errors.New("no content found for the specified date")
	ErrInsufficientContent     = errors.New("insufficient content to generate catch-up lesson")
	ErrOAuthTokenInvalid       = errors.New("oauth token is invalid - user needs to re-authorize")
)

const MinWordCountThreshold = 300

type CatchUpService struct {
	coursesCollection          *mongo.Collection
	studentsCollection         *mongo.Collection
	absenceRecordsCollection   *mongo.Collection
	ingestionJobsCollection    *mongo.Collection
	contentItemsCollection     *mongo.Collection
	extractedContentCollection *mongo.Collection
	catchUpLessonsCollection   *mongo.Collection
	batchJobsCollection        *mongo.Collection
	oauthCollection            *mongo.Collection
	userOAuthService           *UserOAuthService
	config                     *config.Config
}

type googleCourseWork struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Materials    []googleMaterial `json:"materials"`
	CreationTime string           `json:"creationTime"`
	WorkType     string           `json:"workType"`
}

type googleCourseMaterial struct {
	ID           string           `json:"id"`
	Title        string           `json:"title"`
	Description  string           `json:"description"`
	Materials    []googleMaterial `json:"materials"`
	CreationTime string           `json:"creationTime"`
}

type googleAnnouncement struct {
	ID           string           `json:"id"`
	Text         string           `json:"text"`
	Materials    []googleMaterial `json:"materials"`
	CreationTime string           `json:"creationTime"`
}

type googleMaterial struct {
	DriveFile *struct {
		DriveFile struct {
			ID       string `json:"id"`
			Title    string `json:"title"`
			MimeType string `json:"mimeType"`
		} `json:"driveFile"`
	} `json:"driveFile"`
	YouTubeVideo *struct {
		ID    string `json:"id"`
		Title string `json:"title"`
	} `json:"youTubeVideo"`
	Link *struct {
		URL   string `json:"url"`
		Title string `json:"title"`
	} `json:"link"`
	Form *struct {
		FormURL string `json:"formUrl"`
		Title   string `json:"title"`
	} `json:"form"`
}

func NewCatchUpService(client *mongo.Client, dbName string, cfg *config.Config, userOAuthService *UserOAuthService) *CatchUpService {
	db := client.Database(dbName)

	return &CatchUpService{
		coursesCollection:          db.Collection("courses"),
		studentsCollection:         db.Collection("students"),
		absenceRecordsCollection:   db.Collection("absence_records"),
		ingestionJobsCollection:    db.Collection("ingestion_jobs"),
		contentItemsCollection:     db.Collection("content_items"),
		extractedContentCollection: db.Collection("extracted_content"),
		catchUpLessonsCollection:   db.Collection("catchup_lessons"),
		batchJobsCollection:        db.Collection("batch_catchup_jobs"),
		oauthCollection:            db.Collection("oauth_credentials"),
		userOAuthService:           userOAuthService,
		config:                     cfg,
	}
}

type GenerateCatchUpRequest struct {
	CourseID    string   `json:"course_id" binding:"required"`
	StudentIDs  []string `json:"student_ids" binding:"required,min=1"`
	AbsenceDate string   `json:"absence_date" binding:"required"`
}

type GenerateCatchUpResult struct {
	SuccessCount int      `json:"success_count"`
	FailedCount  int      `json:"failed_count"`
	Warnings     []string `json:"warnings,omitempty"`
	Message      string   `json:"message"`
	BatchJobID   string   `json:"batch_job_id,omitempty"` // For async processing
}

// GenerateCatchUpForStudentsAsync initiates async background processing
func (s *CatchUpService) GenerateCatchUpForStudentsAsync(
	ctx context.Context,
	req GenerateCatchUpRequest,
	userID, schoolID string,
) (*GenerateCatchUpResult, error) {
	courseOID, err := bson.ObjectIDFromHex(req.CourseID)
	if err != nil {
		return nil, ErrInvalidCatchUpCourseID
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, errors.New("invalid school id")
	}

	absenceDate, err := time.Parse("2006-01-02", req.AbsenceDate)
	if err != nil {
		return nil, errors.New("invalid date format, use YYYY-MM-DD")
	}

	// Verify course access
	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        courseOID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&course)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCourseNotFound
		}
		return nil, err
	}

	// Verify OAuth credentials exist
	var oauthCred models.OAuthCredential
	err = s.oauthCollection.FindOne(ctx, bson.M{
		"school_id": schoolOID,
		"user_id":   userOID,
	}).Decode(&oauthCred)
	if err != nil {
		return nil, errors.New("oauth credentials not found")
	}

	// Validate student IDs and convert to ObjectIDs
	studentOIDs := make([]bson.ObjectID, 0, len(req.StudentIDs))
	for _, studentIDStr := range req.StudentIDs {
		studentOID, err := bson.ObjectIDFromHex(studentIDStr)
		if err != nil {
			return nil, fmt.Errorf("invalid student ID: %s", studentIDStr)
		}

		// Verify student exists
		count, err := s.studentsCollection.CountDocuments(ctx, bson.M{
			"_id":       studentOID,
			"school_id": schoolOID,
		})
		if err != nil || count == 0 {
			return nil, fmt.Errorf("student not found: %s", studentIDStr)
		}
		studentOIDs = append(studentOIDs, studentOID)
	}

	// Create batch job
	now := time.Now().UTC()
	batchJob := models.BatchCatchUpJob{
		ID:                bson.NewObjectID(),
		SchoolID:          schoolOID,
		CourseID:          courseOID,
		TeacherID:         userOID,
		StudentIDs:        studentOIDs,
		AbsenceDate:       absenceDate,
		Status:            models.BatchJobPending,
		TotalStudents:     len(studentOIDs),
		ProcessedStudents: 0,
		SuccessCount:      0,
		FailedCount:       0,
		Warnings:          []string{},
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	_, err = s.batchJobsCollection.InsertOne(ctx, batchJob)
	if err != nil {
		return nil, fmt.Errorf("failed to create batch job: %w", err)
	}

	// Start background processing
	go s.processBatchJob(batchJob.ID, schoolOID, courseOID, userOID, studentOIDs, absenceDate)

	return &GenerateCatchUpResult{
		Message:    fmt.Sprintf("Processing started for %d student(s)", len(studentOIDs)),
		BatchJobID: batchJob.ID.Hex(),
	}, nil
}

// GetBatchJobStatus retrieves the current status of a batch job
func (s *CatchUpService) GetBatchJobStatus(ctx context.Context, batchJobID, userID, schoolID string) (*models.BatchCatchUpJob, error) {
	jobOID, err := bson.ObjectIDFromHex(batchJobID)
	if err != nil {
		return nil, errors.New("invalid batch job id")
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, errors.New("invalid school id")
	}

	var batchJob models.BatchCatchUpJob
	err = s.batchJobsCollection.FindOne(ctx, bson.M{
		"_id":        jobOID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&batchJob)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, errors.New("batch job not found")
		}
		return nil, err
	}

	return &batchJob, nil
}

// processBatchJob runs in background to process all students
func (s *CatchUpService) processBatchJob(
	batchJobID, schoolID, courseID, teacherID bson.ObjectID,
	studentIDs []bson.ObjectID,
	absenceDate time.Time,
) {
	// Create a new context with long timeout for batch processing
	ctx := context.Background()

	// Update status to processing
	now := time.Now().UTC()
	_, err := s.batchJobsCollection.UpdateOne(ctx,
		bson.M{"_id": batchJobID},
		bson.M{"$set": bson.M{
			"status":     models.BatchJobProcessing,
			"started_at": now,
			"updated_at": now,
		}},
	)
	if err != nil {
		fmt.Printf("Failed to update batch job status: %v\n", err)
		return
	}

	// Get OAuth credentials
	var oauthCred models.OAuthCredential
	err = s.oauthCollection.FindOne(ctx, bson.M{
		"school_id": schoolID,
		"user_id":   teacherID,
	}).Decode(&oauthCred)
	if err != nil {
		s.failBatchJob(ctx, batchJobID, "OAuth credentials not found")
		return
	}

	// Get course info
	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":       courseID,
		"school_id": schoolID,
	}).Decode(&course)
	if err != nil {
		s.failBatchJob(ctx, batchJobID, "Course not found")
		return
	}

	// Track if all failures are due to the same specific error
	var firstError error
	allSameError := true
	hasSuccess := false

	// Process each student
	for _, studentID := range studentIDs {
		err := s.processStudentCatchUp(ctx, schoolID, courseID, studentID, teacherID, absenceDate, &oauthCred, &course)

		updateData := bson.M{
			"$inc": bson.M{"processed_students": 1},
			"$set": bson.M{"updated_at": time.Now().UTC()},
		}

		if err != nil {
			// Check if OAuth error - fail entire batch
			if errors.Is(err, ErrOAuthTokenInvalid) {
				s.failBatchJob(ctx, batchJobID, "oauth_invalid")
				return
			}

			// Track the first error
			if firstError == nil {
				firstError = err
			} else if !errors.Is(err, firstError) &&
				!(errors.Is(err, ErrNoContentFound) && errors.Is(firstError, ErrNoContentFound)) &&
				!(errors.Is(err, ErrInsufficientContent) && errors.Is(firstError, ErrInsufficientContent)) {
				allSameError = false
			}

			// Add warning and increment failed count
			updateData["$inc"].(bson.M)["failed_count"] = 1
			updateData["$push"] = bson.M{
				"warnings": fmt.Sprintf("Failed for student %s: %v", studentID.Hex(), err),
			}
		} else {
			hasSuccess = true
			allSameError = false
			updateData["$inc"].(bson.M)["success_count"] = 1
		}

		_, err = s.batchJobsCollection.UpdateOne(ctx,
			bson.M{"_id": batchJobID},
			updateData,
		)
		if err != nil {
			fmt.Printf("Failed to update batch job progress: %v\n", err)
		}
	}

	// Check if all students failed with the same error
	if !hasSuccess && allSameError && firstError != nil {
		var failureReason string
		if errors.Is(firstError, ErrNoContentFound) {
			failureReason = "no_content_found"
		} else if errors.Is(firstError, ErrInsufficientContent) {
			failureReason = "insufficient_content"
		} else {
			failureReason = firstError.Error()
		}
		s.failBatchJob(ctx, batchJobID, failureReason)
		return
	}

	// Mark batch job as completed
	completedAt := time.Now().UTC()
	_, err = s.batchJobsCollection.UpdateOne(ctx,
		bson.M{"_id": batchJobID},
		bson.M{"$set": bson.M{
			"status":       models.BatchJobCompleted,
			"completed_at": completedAt,
			"updated_at":   completedAt,
		}},
	)
	if err != nil {
		fmt.Printf("Failed to mark batch job as completed: %v\n", err)
	}

	fmt.Printf("Batch job %s completed successfully\n", batchJobID.Hex())
}

// failBatchJob marks a batch job as failed
func (s *CatchUpService) failBatchJob(ctx context.Context, batchJobID bson.ObjectID, reason string) {
	now := time.Now().UTC()
	_, err := s.batchJobsCollection.UpdateOne(ctx,
		bson.M{"_id": batchJobID},
		bson.M{"$set": bson.M{
			"status":         models.BatchJobFailed,
			"failure_reason": reason,
			"completed_at":   now,
			"updated_at":     now,
		}},
	)
	if err != nil {
		fmt.Printf("Failed to mark batch job as failed: %v\n", err)
	}
}

func (s *CatchUpService) GenerateCatchUpForStudents(
	ctx context.Context,
	req GenerateCatchUpRequest,
	userID, schoolID string,
) (*GenerateCatchUpResult, error) {
	courseOID, err := bson.ObjectIDFromHex(req.CourseID)
	if err != nil {
		return nil, ErrInvalidCatchUpCourseID
	}

	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, errors.New("invalid user id")
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, errors.New("invalid school id")
	}

	absenceDate, err := time.Parse("2006-01-02", req.AbsenceDate)
	if err != nil {
		return nil, errors.New("invalid date format, use YYYY-MM-DD")
	}

	var course models.Course
	err = s.coursesCollection.FindOne(ctx, bson.M{
		"_id":        courseOID,
		"school_id":  schoolOID,
		"teacher_id": userOID,
	}).Decode(&course)
	if err != nil {
		if errors.Is(err, mongo.ErrNoDocuments) {
			return nil, ErrCourseNotFound
		}
		return nil, err
	}

	var oauthCred models.OAuthCredential
	err = s.oauthCollection.FindOne(ctx, bson.M{
		"school_id": schoolOID,
		"user_id":   userOID,
	}).Decode(&oauthCred)
	if err != nil {
		return nil, errors.New("oauth credentials not found")
	}

	result := &GenerateCatchUpResult{
		SuccessCount: 0,
		FailedCount:  0,
		Warnings:     []string{},
	}

	// Track if all failures are due to the same specific error
	var firstError error
	allSameError := true

	for _, studentIDStr := range req.StudentIDs {
		studentOID, err := bson.ObjectIDFromHex(studentIDStr)
		if err != nil {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Invalid student ID: %s", studentIDStr))
			allSameError = false
			continue
		}

		count, err := s.studentsCollection.CountDocuments(ctx, bson.M{
			"_id":       studentOID,
			"school_id": schoolOID,
		})
		if err != nil || count == 0 {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Student not found: %s", studentIDStr))
			allSameError = false
			continue
		}

		err = s.processStudentCatchUp(ctx, schoolOID, courseOID, studentOID, userOID, absenceDate, &oauthCred, &course)
		if err != nil {
			// If this is an OAuth error, immediately return it (don't continue processing)
			if errors.Is(err, ErrOAuthTokenInvalid) {
				return nil, ErrOAuthTokenInvalid
			}
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Failed for student %s: %v", studentIDStr, err))

			// Track the first error for potential return
			if firstError == nil {
				firstError = err
			} else if !errors.Is(err, firstError) &&
				!(errors.Is(err, ErrNoContentFound) && errors.Is(firstError, ErrNoContentFound)) &&
				!(errors.Is(err, ErrInsufficientContent) && errors.Is(firstError, ErrInsufficientContent)) {
				allSameError = false
			}
			continue
		}

		result.SuccessCount++
		allSameError = false // At least one success means not all same error
	}

	// If all students failed with the same error, return that error
	if result.SuccessCount == 0 && result.FailedCount > 0 && allSameError && firstError != nil {
		if errors.Is(firstError, ErrNoContentFound) || errors.Is(firstError, ErrInsufficientContent) {
			return nil, firstError
		}
	}

	if result.SuccessCount > 0 {
		result.Message = fmt.Sprintf("Successfully processed %d student(s)", result.SuccessCount)
	} else {
		result.Message = "Failed to process any students"
	}

	return result, nil
}

func (s *CatchUpService) processStudentCatchUp(
	ctx context.Context,
	schoolID, courseID, studentID, teacherID bson.ObjectID,
	absenceDate time.Time,
	oauthCred *models.OAuthCredential,
	course *models.Course,
) error {
	// Create a context with extended timeout for large PDF processing (up to 50 MB)
	// Allows time for: PDF download (2 min) + extraction (3 min) + AI generation (3 min) + buffer (2 min)
	processCtx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
	defer cancel()

	now := time.Now().UTC()

	absenceRecord := models.AbsenceRecord{
		ID:                bson.NewObjectID(),
		SchoolID:          schoolID,
		CourseID:          courseID,
		StudentID:         studentID,
		AbsentOn:          absenceDate,
		MarkedByTeacherID: teacherID,
		Source:            "manual",
		IsLocked:          false,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	_, err := s.absenceRecordsCollection.InsertOne(processCtx, absenceRecord)
	if err != nil {
		return fmt.Errorf("failed to create absence record: %w", err)
	}

	ingestionJob := models.IngestionJob{
		ID:                bson.NewObjectID(),
		SchoolID:          schoolID,
		CourseID:          courseID,
		AbsenceRecordID:   absenceRecord.ID,
		TriggeredByUserID: teacherID,
		Status:            models.IngestionJobPending,
		CreatedAt:         now,
		UpdatedAt:         now,
	}

	_, err = s.ingestionJobsCollection.InsertOne(processCtx, ingestionJob)
	if err != nil {
		return fmt.Errorf("failed to create ingestion job: %w", err)
	}

	startTime := now
	_, err = s.ingestionJobsCollection.UpdateOne(processCtx,
		bson.M{"_id": ingestionJob.ID},
		bson.M{"$set": bson.M{
			"status":     models.IngestionJobRunning,
			"started_at": startTime,
			"updated_at": now,
		}},
	)
	if err != nil {
		return err
	}

	contentItems, err := s.fetchClassroomContent(processCtx, oauthCred, course, absenceDate, schoolID, courseID, ingestionJob.ID)
	if err != nil {
		s.failIngestionJob(processCtx, ingestionJob.ID, err.Error())
		return err
	}

	fmt.Print(contentItems)

	if len(contentItems) == 0 {
		s.failIngestionJob(processCtx, ingestionJob.ID, "no content found for this date")
		return ErrNoContentFound
	}

	extractedContent, err := s.extractAndCombineText(processCtx, schoolID, courseID, absenceRecord.ID, ingestionJob.ID, contentItems, oauthCred)
	if err != nil {
		s.failIngestionJob(processCtx, ingestionJob.ID, err.Error())
		return err
	}

	if !extractedContent.MeetsThreshold {
		s.failIngestionJob(processCtx, ingestionJob.ID, "insufficient content - word count below threshold")
		return ErrInsufficientContent
	}

	catchUpLesson := models.CatchUpLesson{
		ID:                 bson.NewObjectID(),
		SchoolID:           schoolID,
		CourseID:           courseID,
		StudentID:          studentID,
		AbsenceRecordID:    absenceRecord.ID,
		ExtractedContentID: extractedContent.ID,
		Status:             models.CatchUpStatusEmpty,
		Explanation:        "",
		Quiz:               []models.QuizQuestion{},
		RegenerationCount:  0,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	_, err = s.catchUpLessonsCollection.InsertOne(processCtx, catchUpLesson)
	if err != nil {
		s.failIngestionJob(processCtx, ingestionJob.ID, err.Error())
		return err
	}

	// Generate AI content with retry mechanism (up to 3 attempts)
	var aiContent *AIGeneratedContent
	var lastErr error
	maxRetries := 3

	for attempt := 1; attempt <= maxRetries; attempt++ {
		fmt.Printf("AI generation attempt %d/%d for lesson %s\n", attempt, maxRetries, catchUpLesson.ID.Hex())

		aiContent, lastErr = s.generateAIContent(processCtx, extractedContent, course)
		if lastErr == nil {
			// Success - break out of retry loop
			fmt.Printf("AI content generated successfully on attempt %d for lesson %s with title: %s\n", attempt, catchUpLesson.ID.Hex(), aiContent.Title)
			break
		}

		// Log the error
		fmt.Printf("AI generation attempt %d failed for lesson %s: %v\n", attempt, catchUpLesson.ID.Hex(), lastErr)

		// If this wasn't the last attempt, wait before retrying
		if attempt < maxRetries {
			time.Sleep(time.Second * 2) // Wait 2 seconds before retry
		}
	}

	// If all retries failed
	if lastErr != nil {
		fmt.Printf("AI generation failed after %d attempts for lesson %s: %v\n", maxRetries, catchUpLesson.ID.Hex(), lastErr)
		s.failIngestionJob(processCtx, ingestionJob.ID, fmt.Sprintf("AI generation failed after %d attempts: %v", maxRetries, lastErr.Error()))

		// Also update the catch-up lesson to failed status
		s.catchUpLessonsCollection.UpdateOne(processCtx,
			bson.M{"_id": catchUpLesson.ID},
			bson.M{"$set": bson.M{
				"status":     models.CatchUpStatusFailed,
				"updated_at": time.Now().UTC(),
			}},
		)
		return lastErr
	}

	// Update catch-up lesson with AI-generated content
	generatedTime := time.Now().UTC()
	_, err = s.catchUpLessonsCollection.UpdateOne(processCtx,
		bson.M{"_id": catchUpLesson.ID},
		bson.M{"$set": bson.M{
			"title":               aiContent.Title,
			"explanation":         aiContent.Explanation,
			"learning_objectives": aiContent.LearningObjectives,
			"quiz":                aiContent.Quiz,
			"status":              models.CatchUpStatusGenerated,
			"generation_model":    "gpt-4o-mini",
			"prompt_version":      "v1.1",
			"generated_at":        generatedTime,
			"updated_at":          generatedTime,
		}},
	)
	if err != nil {
		s.failIngestionJob(processCtx, ingestionJob.ID, err.Error())
		return err
	}

	completedTime := time.Now().UTC()
	_, err = s.ingestionJobsCollection.UpdateOne(processCtx,
		bson.M{"_id": ingestionJob.ID},
		bson.M{"$set": bson.M{
			"status":       models.IngestionJobCompleted,
			"completed_at": completedTime,
			"updated_at":   completedTime,
		}},
	)

	return err
}

func (s *CatchUpService) failIngestionJob(ctx context.Context, jobID bson.ObjectID, reason string) {
	fmt.Printf("Failing ingestion job %s with reason: %s\n", jobID.Hex(), reason)

	// Create a fresh context with timeout to ensure this critical update succeeds
	// even if the parent context has expired (e.g., during large PDF processing)
	updateCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err := s.ingestionJobsCollection.UpdateOne(updateCtx,
		bson.M{"_id": jobID},
		bson.M{"$set": bson.M{
			"status":         models.IngestionJobFailed,
			"failure_reason": reason,
			"updated_at":     time.Now().UTC(),
		}},
	)
	if err != nil {
		fmt.Printf("Failed to update ingestion job status: %v\n", err)
	}
}

func (s *CatchUpService) fetchClassroomContent(
	ctx context.Context,
	oauthCred *models.OAuthCredential,
	course *models.Course,
	absenceDate time.Time,
	schoolID, courseID, ingestionJobID bson.ObjectID,
) ([]models.ContentItem, error) {
	var externalCourseID string
	for _, ref := range course.ExternalRefs {
		if ref.Provider == models.ProviderGoogleClassroom {
			externalCourseID = ref.ExternalID
			break
		}
	}

	if externalCourseID == "" {
		return nil, errors.New("course not synced from Google Classroom")
	}

	client, err := s.createOAuthClient(ctx, oauthCred)
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	contentItems := []models.ContentItem{}

	courseWork, err := s.fetchCourseWork(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch coursework: %w", err)
	}

	for _, cw := range courseWork {
		item := s.courseWorkToContentItem(cw, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	materials, err := s.fetchCourseMaterials(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch materials: %w", err)
	}

	for _, mat := range materials {
		item := s.materialToContentItem(mat, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	announcements, err := s.fetchAnnouncements(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch announcements: %w", err)
	}

	for _, ann := range announcements {
		item := s.announcementToContentItem(ann, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	return contentItems, nil
}

func (s *CatchUpService) fetchCourseWork(ctx context.Context, client *http.Client, courseID string, targetDate time.Time) ([]googleCourseWork, error) {
	baseURL := fmt.Sprintf("https://classroom.googleapis.com/v1/courses/%s/courseWork", courseID)

	var allWork []googleCourseWork
	pageToken := ""

	for {
		reqURL := baseURL
		if pageToken != "" {
			reqURL += "?pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("classroom API error: %d - %s", resp.StatusCode, string(body))
		}

		var result struct {
			CourseWork    []googleCourseWork `json:"courseWork"`
			NextPageToken string             `json:"nextPageToken"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		for _, cw := range result.CourseWork {
			creationTime, err := time.Parse(time.RFC3339, cw.CreationTime)
			if err != nil {
				continue
			}

			// Skip assignments created by our catch-up system
			if isSameDay(creationTime, targetDate) && !isCatchUpAssignment(cw) {
				allWork = append(allWork, cw)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	return allWork, nil
}

func (s *CatchUpService) fetchCourseMaterials(ctx context.Context, client *http.Client, courseID string, targetDate time.Time) ([]googleCourseMaterial, error) {
	baseURL := fmt.Sprintf("https://classroom.googleapis.com/v1/courses/%s/courseWorkMaterials", courseID)

	var allMaterials []googleCourseMaterial
	pageToken := ""

	for {
		reqURL := baseURL
		if pageToken != "" {
			reqURL += "?pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("classroom API error: %d - %s", resp.StatusCode, string(body))
		}

		var result struct {
			CourseWorkMaterial []googleCourseMaterial `json:"courseWorkMaterial"`
			NextPageToken      string                 `json:"nextPageToken"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		for _, mat := range result.CourseWorkMaterial {
			creationTime, err := time.Parse(time.RFC3339, mat.CreationTime)
			if err != nil {
				continue
			}

			if isSameDay(creationTime, targetDate) {
				allMaterials = append(allMaterials, mat)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	return allMaterials, nil
}

func (s *CatchUpService) fetchAnnouncements(ctx context.Context, client *http.Client, courseID string, targetDate time.Time) ([]googleAnnouncement, error) {
	baseURL := fmt.Sprintf("https://classroom.googleapis.com/v1/courses/%s/announcements", courseID)

	var allAnnouncements []googleAnnouncement
	pageToken := ""

	for {
		reqURL := baseURL
		if pageToken != "" {
			reqURL += "?pageToken=" + url.QueryEscape(pageToken)
		}

		req, err := http.NewRequestWithContext(ctx, "GET", reqURL, nil)
		if err != nil {
			return nil, err
		}

		resp, err := client.Do(req)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("classroom API error: %d - %s", resp.StatusCode, string(body))
		}

		var result struct {
			Announcements []googleAnnouncement `json:"announcements"`
			NextPageToken string               `json:"nextPageToken"`
		}

		if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
			return nil, err
		}

		for _, ann := range result.Announcements {
			creationTime, err := time.Parse(time.RFC3339, ann.CreationTime)
			if err != nil {
				continue
			}

			if isSameDay(creationTime, targetDate) {
				allAnnouncements = append(allAnnouncements, ann)
			}
		}

		if result.NextPageToken == "" {
			break
		}
		pageToken = result.NextPageToken
	}

	return allAnnouncements, nil
}

func (s *CatchUpService) courseWorkToContentItem(cw googleCourseWork, schoolID, courseID, ingestionJobID bson.ObjectID, now time.Time) models.ContentItem {
	attachments := s.parseMaterials(cw.Materials)
	included := len(attachments) > 0 && hasTextContent(cw.Description, attachments)

	item := models.ContentItem{
		ID:             bson.NewObjectID(),
		SchoolID:       schoolID,
		CourseID:       courseID,
		IngestionJobID: ingestionJobID,
		Type:           models.ContentTypeAssignment,
		Title:          cw.Title,
		Description:    cw.Description,
		Attachments:    attachments,
		Included:       included,
		ExcludedNotes:  []string{},
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:   models.ProviderGoogleClassroom,
			ExternalID: cw.ID,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if !included {
		item.ExcludedNotes = append(item.ExcludedNotes, "No supported attachments or insufficient text")
	}

	return item
}

func (s *CatchUpService) materialToContentItem(mat googleCourseMaterial, schoolID, courseID, ingestionJobID bson.ObjectID, now time.Time) models.ContentItem {
	attachments := s.parseMaterials(mat.Materials)
	included := len(attachments) > 0 && hasTextContent(mat.Description, attachments)

	item := models.ContentItem{
		ID:             bson.NewObjectID(),
		SchoolID:       schoolID,
		CourseID:       courseID,
		IngestionJobID: ingestionJobID,
		Type:           models.ContentTypeMaterial,
		Title:          mat.Title,
		Description:    mat.Description,
		Attachments:    attachments,
		Included:       included,
		ExcludedNotes:  []string{},
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:   models.ProviderGoogleClassroom,
			ExternalID: mat.ID,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if !included {
		item.ExcludedNotes = append(item.ExcludedNotes, "No supported attachments or insufficient text")
	}

	return item
}

func (s *CatchUpService) announcementToContentItem(ann googleAnnouncement, schoolID, courseID, ingestionJobID bson.ObjectID, now time.Time) models.ContentItem {
	attachments := s.parseMaterials(ann.Materials)
	included := len(ann.Text) > 0 || (len(attachments) > 0 && hasTextContent(ann.Text, attachments))

	item := models.ContentItem{
		ID:             bson.NewObjectID(),
		SchoolID:       schoolID,
		CourseID:       courseID,
		IngestionJobID: ingestionJobID,
		Type:           models.ContentTypeAnnouncement,
		Title:          "Announcement",
		Description:    ann.Text,
		Attachments:    attachments,
		Included:       included,
		ExcludedNotes:  []string{},
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:   models.ProviderGoogleClassroom,
			ExternalID: ann.ID,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	if !included {
		item.ExcludedNotes = append(item.ExcludedNotes, "No text content or supported attachments")
	}

	return item
}

func (s *CatchUpService) parseMaterials(materials []googleMaterial) []models.ContentAttachment {
	var attachments []models.ContentAttachment

	for _, mat := range materials {
		if mat.DriveFile != nil {
			df := mat.DriveFile.DriveFile
			mimeType := df.MimeType

			// If MIME type is empty, it means we need to fetch it from Drive API
			// For now, we'll mark it as needing metadata, but we'll try to extract it anyway
			if mimeType == "" {
				mimeType = "application/octet-stream"
			}

			kind, isSupported := classifyDriveFile(mimeType, df.Title)

			att := models.ContentAttachment{
				Title:       df.Title,
				URL:         fmt.Sprintf("https://drive.google.com/file/d/%s/view", df.ID),
				MimeType:    mimeType,
				Kind:        kind,
				IsSupported: isSupported,
				ExternalID:  df.ID,
			}

			if !isSupported {
				if mimeType == "application/octet-stream" || mimeType == "" {
					att.ExcludeCause = "MIME type not provided by Classroom API - will attempt extraction"
					// Still mark as potentially supported for extraction attempt
					att.IsSupported = true
					att.Kind = models.AttachmentKindPDF // Assume PDF for extraction
				} else {
					att.ExcludeCause = fmt.Sprintf("Unsupported file type: %s", mimeType)
				}
			}

			attachments = append(attachments, att)
		}

		if mat.YouTubeVideo != nil {
			attachments = append(attachments, models.ContentAttachment{
				Title:        mat.YouTubeVideo.Title,
				URL:          fmt.Sprintf("https://www.youtube.com/watch?v=%s", mat.YouTubeVideo.ID),
				Kind:         models.AttachmentKindVideo,
				IsSupported:  false,
				ExcludeCause: "YouTube videos not supported for text extraction",
				ExternalID:   mat.YouTubeVideo.ID,
			})
		}

		if mat.Link != nil {
			attachments = append(attachments, models.ContentAttachment{
				Title:        mat.Link.Title,
				URL:          mat.Link.URL,
				Kind:         models.AttachmentKindExternalURL,
				IsSupported:  false,
				ExcludeCause: "External links not supported",
			})
		}

		if mat.Form != nil {
			attachments = append(attachments, models.ContentAttachment{
				Title:        mat.Form.Title,
				URL:          mat.Form.FormURL,
				Kind:         models.AttachmentKindOther,
				IsSupported:  false,
				ExcludeCause: "Google Forms not supported",
			})
		}
	}

	return attachments
}

func classifyDriveFile(mimeType, fileName string) (models.AttachmentKind, bool) {
	mimeType = strings.ToLower(mimeType)
	fileName = strings.ToLower(fileName)

	switch {
	case strings.Contains(mimeType, "google-apps.document") || mimeType == "application/vnd.google-apps.document":
		return models.AttachmentKindGoogleDoc, true
	case strings.Contains(mimeType, "google-apps.presentation") || mimeType == "application/vnd.google-apps.presentation":
		return models.AttachmentKindGoogleSlide, true
	case strings.Contains(mimeType, "pdf") || mimeType == "application/pdf":
		return models.AttachmentKindPDF, true
	case strings.Contains(mimeType, "image"):
		return models.AttachmentKindImage, false
	case strings.Contains(mimeType, "video"):
		return models.AttachmentKindVideo, false
	}

	// If MIME type is empty or unknown, try file extension
	if mimeType == "" || mimeType == "application/octet-stream" {
		if strings.HasSuffix(fileName, ".pdf") {
			return models.AttachmentKindPDF, true
		}
		if strings.HasSuffix(fileName, ".doc") || strings.HasSuffix(fileName, ".docx") {
			return models.AttachmentKindPDF, false
		}
		if strings.HasSuffix(fileName, ".ppt") || strings.HasSuffix(fileName, ".pptx") {
			return models.AttachmentKindPDF, false
		}
		// For Google Drive URLs without extension, mark as needing metadata fetch
		return models.AttachmentKindOther, false
	}

	if strings.HasSuffix(fileName, ".pdf") {
		return models.AttachmentKindPDF, true
	}
	if strings.HasSuffix(fileName, ".doc") || strings.HasSuffix(fileName, ".docx") {
		return models.AttachmentKindPDF, false
	}
	if strings.HasSuffix(fileName, ".ppt") || strings.HasSuffix(fileName, ".pptx") {
		return models.AttachmentKindPDF, false
	}
	if strings.HasSuffix(fileName, ".jpg") || strings.HasSuffix(fileName, ".jpeg") ||
		strings.HasSuffix(fileName, ".png") || strings.HasSuffix(fileName, ".gif") {
		return models.AttachmentKindImage, false
	}
	if strings.HasSuffix(fileName, ".mp4") || strings.HasSuffix(fileName, ".avi") ||
		strings.HasSuffix(fileName, ".mov") || strings.HasSuffix(fileName, ".wmv") {
		return models.AttachmentKindVideo, false
	}

	return models.AttachmentKindOther, false
}

func hasTextContent(description string, attachments []models.ContentAttachment) bool {
	if len(strings.TrimSpace(description)) > 50 {
		return true
	}

	for _, att := range attachments {
		if att.IsSupported {
			return true
		}
	}

	return false
}

func isSameDay(t1, t2 time.Time) bool {
	y1, m1, d1 := t1.Date()
	y2, m2, d2 := t2.Date()
	return y1 == y2 && m1 == m2 && d1 == d2
}

// isCatchUpAssignment checks if a courseWork item was created by our catch-up system
func isCatchUpAssignment(cw googleCourseWork) bool {
	// Check if description starts with our catch-up template
	if strings.HasPrefix(cw.Description, "Please review the attached PDF for the catch-up lesson") {
		return true
	}

	// Check if any material has a PDF with the catch-up naming pattern
	for _, material := range cw.Materials {
		if material.DriveFile != nil {
			title := material.DriveFile.DriveFile.Title
			if strings.HasPrefix(title, "Catch-Up_") && strings.HasSuffix(title, ".pdf") {
				return true
			}
		}
	}

	return false
}

func (s *CatchUpService) createOAuthClient(ctx context.Context, oauthCred *models.OAuthCredential) (*http.Client, error) {
	if oauthCred.RefreshTokenEnc == "" {
		return nil, ErrOAuthTokenInvalid
	}

	// Use centralized refresh method from UserOAuthService
	// This method automatically refreshes the token if needed and marks as invalid if refresh fails
	client, err := s.userOAuthService.RefreshOAuthToken(ctx, oauthCred)
	if err != nil {
		// RefreshOAuthToken already marked the credential as invalid in the database
		// Wrap the error so it can be identified as an OAuth error
		return nil, fmt.Errorf("%w: %v", ErrOAuthTokenInvalid, err)
	}

	return client, nil
}

func (s *CatchUpService) extractTextFromAttachment(
	ctx context.Context,
	oauthCred *models.OAuthCredential,
	att models.ContentAttachment,
) (string, error) {
	if att.ExternalID == "" {
		return "", errors.New("no external ID for attachment")
	}

	client, err := s.createOAuthClient(ctx, oauthCred)
	if err != nil {
		return "", err
	}

	// If MIME type is unknown, fetch file metadata from Drive API
	actualKind := att.Kind
	if att.MimeType == "" || att.MimeType == "application/octet-stream" {
		metadata, err := s.fetchDriveFileMetadata(ctx, client, att.ExternalID)
		if err == nil && metadata.MimeType != "" {
			actualKind, _ = classifyDriveFile(metadata.MimeType, metadata.Name)
		}
	}

	switch actualKind {
	case models.AttachmentKindGoogleDoc:
		return s.extractFromGoogleDoc(ctx, client, att.ExternalID)
	case models.AttachmentKindGoogleSlide:
		return s.extractFromGoogleSlides(ctx, client, att.ExternalID)
	case models.AttachmentKindPDF:
		return s.extractFromPDF(ctx, client, att.ExternalID)
	default:
		return "", fmt.Errorf("unsupported attachment kind: %s", actualKind)
	}
}

func (s *CatchUpService) fetchDriveFileMetadata(ctx context.Context, client *http.Client, fileID string) (*struct {
	Name     string `json:"name"`
	MimeType string `json:"mimeType"`
}, error) {
	url := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?fields=name,mimeType", fileID)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("drive API error: %d - %s", resp.StatusCode, string(body))
	}

	var metadata struct {
		Name     string `json:"name"`
		MimeType string `json:"mimeType"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&metadata); err != nil {
		return nil, err
	}

	return &metadata, nil
}

func (s *CatchUpService) extractFromGoogleDoc(ctx context.Context, client *http.Client, fileID string) (string, error) {
	exportURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/export?mimeType=text/plain", fileID)

	req, err := http.NewRequestWithContext(ctx, "GET", exportURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("drive API error: %d - %s", resp.StatusCode, string(body))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *CatchUpService) extractFromGoogleSlides(ctx context.Context, client *http.Client, fileID string) (string, error) {
	exportURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s/export?mimeType=text/plain", fileID)

	req, err := http.NewRequestWithContext(ctx, "GET", exportURL, nil)
	if err != nil {
		return "", err
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("drive API error: %d - %s", resp.StatusCode, string(body))
	}

	content, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	return string(content), nil
}

func (s *CatchUpService) extractFromPDF(ctx context.Context, client *http.Client, fileID string) (string, error) {
	downloadURL := fmt.Sprintf("https://www.googleapis.com/drive/v3/files/%s?alt=media", fileID)

	req, err := http.NewRequestWithContext(ctx, "GET", downloadURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create download request: %w", err)
	}

	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("failed to download PDF: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("drive API error downloading PDF: %d - %s", resp.StatusCode, string(body))
	}

	// Check PDF size before downloading (max 50 MB)
	contentLength := resp.ContentLength
	if contentLength > 50*1024*1024 {
		return "", fmt.Errorf("PDF file too large: %d bytes (max 50 MB)", contentLength)
	}

	pdfData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF data: %w", err)
	}

	fmt.Printf("Downloaded PDF, size: %d bytes (%.2f MB)\n", len(pdfData), float64(len(pdfData))/(1024*1024))

	text, err := s.extractTextFromPDFBytes(pdfData)
	if err == nil && len(strings.Fields(text)) >= 50 {
		return text, nil
	}

	// Check if we got good results from standard extraction
	pdfSizeMB := float64(len(pdfData)) / (1024 * 1024)
	wordCount := len(strings.Fields(text))

	fmt.Printf("Standard PDF extraction: %.2f MB, %d words extracted\n", pdfSizeMB, wordCount)

	// If standard extraction got good results (>=50 words), return immediately
	if wordCount >= 50 {
		fmt.Printf("Standard extraction successful with %d words\n", wordCount)
		return text, nil
	}

	// Standard extraction yielded insufficient text - try OCR fallback
	fmt.Printf("Standard extraction insufficient (%d words), attempting OCR...\n", wordCount)

	// Try Tesseract OCR for scanned/image-based PDFs
	if s.isTesseractAvailable() {
		fmt.Println("Using Tesseract OCR for text extraction...")
		ocrText, ocrErr := s.extractWithTesseractOCR(ctx, pdfData)
		if ocrErr == nil && len(strings.Fields(ocrText)) > wordCount {
			fmt.Printf("Tesseract OCR successful: %d words extracted\n", len(strings.Fields(ocrText)))
			return ocrText, nil
		}
		if ocrErr != nil {
			fmt.Printf("Tesseract OCR failed: %v\n", ocrErr)
		}
	}

	// For smaller PDFs (<= 10 MB), try OpenAI Vision as last resort
	if pdfSizeMB <= 10 && s.config.OpenAIAPIKey != "" {
		fmt.Printf("Trying OpenAI Vision API as fallback (PDF size: %.2f MB)...\n", pdfSizeMB)
		aiText, aiErr := s.extractWithOpenAIVision(ctx, pdfData, fileID)
		if aiErr == nil && aiText != "" {
			fmt.Printf("OpenAI Vision successful: %d words extracted\n", len(strings.Fields(aiText)))
			return aiText, nil
		}
		if aiErr != nil {
			fmt.Printf("OpenAI Vision fallback failed: %v\n", aiErr)
		}
	}

	// Return whatever text we have, even if insufficient
	if text != "" {
		fmt.Printf("Returning partial extraction: %d words\n", wordCount)
		return text, nil
	}

	return "", fmt.Errorf("PDF text extraction failed: PDF appears to be scanned/image-based. Standard extraction: %d words, OCR: failed, OpenAI: unavailable/failed", wordCount)
}

func (s *CatchUpService) extractTextFromPDFBytes(pdfData []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "pdf-extract-*.pdf")
	if err != nil {
		return "", fmt.Errorf("failed to create temp file: %w", err)
	}
	defer os.Remove(tmpFile.Name())
	defer tmpFile.Close()

	if _, err := tmpFile.Write(pdfData); err != nil {
		return "", fmt.Errorf("failed to write PDF to temp file: %w", err)
	}

	tmpFile.Close()

	f, pdfReader, err := pdf.Open(tmpFile.Name())
	if err != nil {
		return "", fmt.Errorf("failed to open PDF: %w", err)
	}
	defer f.Close()

	var textParts []string
	totalPages := pdfReader.NumPage()

	// Limit to first 200 pages for large PDFs to prevent excessive processing
	maxPages := totalPages
	if maxPages > 200 {
		fmt.Printf("PDF has %d pages, limiting extraction to first 200 pages\n", totalPages)
		maxPages = 200
	}

	for pageNum := 1; pageNum <= maxPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		pageText, err := page.GetPlainText(nil)
		if err != nil {
			// Log but continue - don't fail on individual page errors
			fmt.Printf("Warning: Failed to extract text from page %d: %v\n", pageNum, err)
			continue
		}

		if pageText != "" {
			textParts = append(textParts, pageText)
		}
	}

	combinedText := strings.Join(textParts, "\n\n")
	wordCount := len(strings.Fields(combinedText))

	fmt.Printf("PDF text extraction complete: %d pages processed, %d words extracted\n", len(textParts), wordCount)

	return combinedText, nil
}

// isTesseractAvailable checks if Tesseract OCR is installed on the system
func (s *CatchUpService) isTesseractAvailable() bool {
	cmd := exec.Command("tesseract", "--version")
	err := cmd.Run()
	return err == nil
}

// extractWithTesseractOCR extracts text from a PDF using Tesseract OCR
// This is useful for scanned/image-based PDFs where standard text extraction fails
func (s *CatchUpService) extractWithTesseractOCR(ctx context.Context, pdfData []byte) (string, error) {
	// Convert PDF to images
	images, err := s.convertPDFToImages(pdfData)
	if err != nil {
		return "", fmt.Errorf("failed to convert PDF to images: %w", err)
	}

	if len(images) == 0 {
		return "", errors.New("no images extracted from PDF")
	}

	fmt.Printf("Converted PDF to %d images for OCR processing\n", len(images))

	// Initialize Tesseract client
	client := gosseract.NewClient()
	defer client.Close()

	// Set language to English (you can make this configurable)
	client.SetLanguage("eng")

	var textParts []string

	// OCR each image (limit to first 50 pages to prevent excessive processing)
	maxPages := len(images)
	if maxPages > 50 {
		fmt.Printf("Limiting OCR to first 50 pages (PDF has %d pages)\n", len(images))
		maxPages = 50
	}

	for i := 0; i < maxPages; i++ {
		// Save image to temporary file
		tmpImg, err := os.CreateTemp("", fmt.Sprintf("ocr-page-%d-*.png", i))
		if err != nil {
			fmt.Printf("Failed to create temp file for page %d: %v\n", i, err)
			continue
		}

		// Write PNG image
		err = png.Encode(tmpImg, images[i])
		tmpImg.Close()

		if err != nil {
			fmt.Printf("Failed to write image for page %d: %v\n", i, err)
			os.Remove(tmpImg.Name())
			continue
		}

		// Perform OCR
		client.SetImage(tmpImg.Name())
		pageText, err := client.Text()

		// Clean up temp file
		os.Remove(tmpImg.Name())

		if err != nil {
			fmt.Printf("OCR failed for page %d: %v\n", i+1, err)
			continue
		}

		if strings.TrimSpace(pageText) != "" {
			textParts = append(textParts, pageText)
			fmt.Printf("OCR page %d: %d words extracted\n", i+1, len(strings.Fields(pageText)))
		}
	}

	if len(textParts) == 0 {
		return "", errors.New("OCR yielded no text from any page")
	}

	combinedText := strings.Join(textParts, "\n\n")
	fmt.Printf("Total OCR extraction: %d pages, %d words\n", len(textParts), len(strings.Fields(combinedText)))

	return combinedText, nil
}

// convertPDFToImages converts a PDF to a slice of images (one per page)
func (s *CatchUpService) convertPDFToImages(pdfData []byte) ([]image.Image, error) {
	// Save PDF to temporary file
	tmpPDF, err := os.CreateTemp("", "pdf-to-img-*.pdf")
	if err != nil {
		return nil, fmt.Errorf("failed to create temp PDF file: %w", err)
	}
	defer os.Remove(tmpPDF.Name())

	if _, err := tmpPDF.Write(pdfData); err != nil {
		tmpPDF.Close()
		return nil, fmt.Errorf("failed to write PDF data: %w", err)
	}
	tmpPDF.Close()

	// Open PDF with go-fitz (MuPDF bindings)
	doc, err := fitz.New(tmpPDF.Name())
	if err != nil {
		return nil, fmt.Errorf("failed to open PDF with fitz: %w", err)
	}
	defer doc.Close()

	numPages := doc.NumPage()
	if numPages == 0 {
		return nil, errors.New("PDF has no pages")
	}

	fmt.Printf("PDF has %d pages, converting to images...\n", numPages)

	var images []image.Image

	// Limit to first 50 pages for large PDFs
	maxPages := numPages
	if maxPages > 50 {
		maxPages = 50
	}

	for i := 0; i < maxPages; i++ {
		// Render page to image at 150 DPI (good balance of quality vs size)
		img, err := doc.Image(i)
		if err != nil {
			fmt.Printf("Failed to render page %d: %v\n", i+1, err)
			continue
		}

		images = append(images, img)
	}

	return images, nil
}

func (s *CatchUpService) extractWithOpenAIVision(ctx context.Context, fileData []byte, fileID string) (string, error) {
	if s.config.OpenAIAPIKey == "" {
		return "", errors.New("OpenAI API key not configured")
	}

	aiClient := openai.NewClient(s.config.OpenAIAPIKey)

	base64Data := base64.StdEncoding.EncodeToString(fileData)
	dataURL := fmt.Sprintf("data:application/pdf;base64,%s", base64Data)

	req := openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{
				Role: openai.ChatMessageRoleUser,
				MultiContent: []openai.ChatMessagePart{
					{
						Type: openai.ChatMessagePartTypeText,
						Text: "Extract all text content from this document. Return only the text, preserving structure and formatting as much as possible. Include all readable text from all pages.",
					},
					{
						Type: openai.ChatMessagePartTypeImageURL,
						ImageURL: &openai.ChatMessageImageURL{
							URL:    dataURL,
							Detail: openai.ImageURLDetailAuto,
						},
					},
				},
			},
		},
		MaxTokens: 4000,
	}

	resp, err := aiClient.CreateChatCompletion(ctx, req)
	if err != nil {
		return "", fmt.Errorf("OpenAI API error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return "", errors.New("no response from OpenAI")
	}

	extractedText := resp.Choices[0].Message.Content
	return strings.TrimSpace(extractedText), nil
}

func (s *CatchUpService) extractAndCombineText(
	ctx context.Context,
	schoolID, courseID, absenceRecordID, ingestionJobID bson.ObjectID,
	contentItems []models.ContentItem,
	oauthCred *models.OAuthCredential,
) (*models.ExtractedContent, error) {
	now := time.Now().UTC()

	var combinedParts []string
	var contentItemIDs []bson.ObjectID
	warnings := []string{}

	for _, item := range contentItems {
		if !item.Included {
			continue
		}

		contentItemIDs = append(contentItemIDs, item.ID)

		if item.Title != "" {
			combinedParts = append(combinedParts, item.Title)
		}
		if item.Description != "" {
			combinedParts = append(combinedParts, item.Description)
		}

		for _, att := range item.Attachments {
			if !att.IsSupported {
				warnings = append(warnings, fmt.Sprintf("Unsupported attachment: %s (%s)", att.Title, att.Kind))
				continue
			}

			text, err := s.extractTextFromAttachment(ctx, oauthCred, att)
			if err != nil {
				warnings = append(warnings, fmt.Sprintf("Failed to extract text from %s: %v", att.Title, err))
				continue
			}

			if text != "" {
				combinedParts = append(combinedParts, text)
			}
		}
	}

	combinedText := strings.Join(combinedParts, "\n\n")
	wordCount := len(strings.Fields(combinedText))
	meetsThreshold := wordCount >= MinWordCountThreshold

	if wordCount < MinWordCountThreshold {
		warnings = append(warnings, fmt.Sprintf("Word count (%d) below minimum threshold (%d)", wordCount, MinWordCountThreshold))
	}

	extractedContent := models.ExtractedContent{
		ID:                   bson.NewObjectID(),
		SchoolID:             schoolID,
		CourseID:             courseID,
		AbsenceRecordID:      absenceRecordID,
		IngestionJobID:       ingestionJobID,
		ContentItemIDs:       contentItemIDs,
		CombinedText:         combinedText,
		WordCount:            wordCount,
		MinWordCountRequired: MinWordCountThreshold,
		MeetsThreshold:       meetsThreshold,
		Warnings:             warnings,
		CreatedAt:            now,
		UpdatedAt:            now,
	}

	_, err := s.extractedContentCollection.InsertOne(ctx, extractedContent)
	if err != nil {
		return nil, err
	}

	return &extractedContent, nil
}

type AIGeneratedContent struct {
	Title              string
	Explanation        string
	LearningObjectives []string
	Quiz               []models.QuizQuestion
}

func (s *CatchUpService) generateAIContent(ctx context.Context, extractedContent *models.ExtractedContent, course *models.Course) (*AIGeneratedContent, error) {
	if s.config.OpenAIAPIKey == "" {
		return nil, errors.New("OpenAI API key not configured")
	}

	fmt.Printf("Starting AI content generation for course: %s, word count: %d\n", course.Name, extractedContent.WordCount)

	client := openai.NewClient(s.config.OpenAIAPIKey)

	prompt := fmt.Sprintf(`You are an educational AI assistant helping create a catch-up lesson for a student who missed class.

Course: %s

Content the student missed (extracted from class materials):
%s

Your task:
1. Create a concise, engaging title for this catch-up lesson (max 60 characters)
   - Should clearly indicate the topic covered
   - Make it student-friendly and descriptive
   - Example: "Photosynthesis & Cell Respiration Overview"

2. Create a clear, structured explanation of what the student missed
   - Format the explanation as clean, semantic HTML
   - Use <h2> for main section headings
   - Use <h3> for subsection headings
   - Use <p> for paragraphs
   - Use <ul> and <li> for bullet points
   - Use <ol> and <li> for numbered lists
   - Use <strong> for key terms or important concepts
   - Use <em> for emphasis where appropriate
   - Make it engaging and easy to understand
   - Do NOT include any <script>, <style>, or potentially unsafe HTML tags

3. Generate 5-7 learning objectives that the student should achieve after reviewing this content

4. Create a quiz with 5-7 questions to check understanding
   - Mix of multiple choice (4 options each) and short answer questions
   - Questions should test comprehension of the key concepts
   - Include the correct answer for each question

Please respond in the following JSON format:
{
  "title": "Concise lesson title",
  "explanation": "<h2>Section Title</h2><p>Well-structured HTML explanation...</p><ul><li>Point 1</li></ul>",
  "learning_objectives": ["Objective 1", "Objective 2", ...],
  "quiz": [
    {
      "question": "Question text",
      "type": "mcq",
      "options": ["Option A", "Option B", "Option C", "Option D"],
      "answer": "Option B"
    },
    {
      "question": "Question text",
      "type": "short_answer",
      "answer": "Expected answer"
    }
  ]
}

IMPORTANT: The "explanation" field must contain valid HTML markup. Use semantic HTML tags to structure the content properly.`, course.Name, extractedContent.CombinedText)

	resp, err := client.CreateChatCompletion(ctx, openai.ChatCompletionRequest{
		Model: openai.GPT4oMini,
		Messages: []openai.ChatCompletionMessage{
			{
				Role:    openai.ChatMessageRoleSystem,
				Content: "You are an expert educational content creator specializing in creating engaging catch-up lessons for students. Always wrap your JSON response in a code block starting with ```json and ending with ```.",
			},
			{
				Role:    openai.ChatMessageRoleUser,
				Content: prompt,
			},
		},
		Temperature: 0.7,
		MaxTokens:   2000,
	})

	if err != nil {
		return nil, fmt.Errorf("OpenAI API error: %w", err)
	}

	if len(resp.Choices) == 0 {
		return nil, errors.New("no response from OpenAI")
	}

	content := resp.Choices[0].Message.Content
	fmt.Printf("Received OpenAI response, length: %d bytes\n", len(content))

	// Strip the ```json ... ``` code block wrapper
	content = strings.TrimSpace(content)
	content = strings.TrimPrefix(content, "```json")
	content = strings.TrimSuffix(content, "```")
	content = strings.TrimSpace(content)

	var aiResponse struct {
		Title              string   `json:"title"`
		Explanation        string   `json:"explanation"`
		LearningObjectives []string `json:"learning_objectives"`
		Quiz               []struct {
			Question string   `json:"question"`
			Type     string   `json:"type"`
			Options  []string `json:"options,omitempty"`
			Answer   string   `json:"answer"`
		} `json:"quiz"`
	}

	err = json.Unmarshal([]byte(content), &aiResponse)
	if err != nil {
		fmt.Printf("Failed to parse AI response as JSON: %v\n", err)
		fmt.Printf("Raw content (first 500 chars): %s\n", content[:min(500, len(content))])
		return nil, fmt.Errorf("failed to parse AI response: %w", err)
	}

	fmt.Printf("Successfully parsed AI response. Title: %s, Quiz questions: %d\n", aiResponse.Title, len(aiResponse.Quiz))

	quiz := make([]models.QuizQuestion, len(aiResponse.Quiz))
	for i, q := range aiResponse.Quiz {
		questionType := models.QuizQuestionMCQ
		if q.Type == "short_answer" {
			questionType = models.QuizQuestionShortAnswer
		}

		quiz[i] = models.QuizQuestion{
			Question: q.Question,
			Type:     questionType,
			Options:  q.Options,
			Answer:   q.Answer,
		}
	}

	return &AIGeneratedContent{
		Title:              aiResponse.Title,
		Explanation:        aiResponse.Explanation,
		LearningObjectives: aiResponse.LearningObjectives,
		Quiz:               quiz,
	}, nil
}
