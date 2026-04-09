package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"image"
	"image/png"
	"io"
	"math/rand"
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

const (
	MinWordCountThreshold       = 100              // Lowered from 300 — announcements with mixed content typically have 50–150 words
	ContentFetchTimeoutSecs     = 120              // Per-file download/extraction timeout for non-PDF attachments
	LargePDFTimeoutSecs         = 600              // 10-minute timeout for PDF downloads (large textbooks can be 50 MB+)
	LargePDFThresholdBytes      = 15 * 1024 * 1024 // 15 MB — sample pages for PDFs larger than this
	LargePDFMaxDownloadBytes    = 50 * 1024 * 1024 // 50 MB — threshold for very large PDFs
	LargePDFSamplePageCount     = 6                // Number of evenly distributed pages to sample from 15–50 MB PDFs
	VeryLargePDFSamplePageCount = 10               // Number of random pages to sample from 50+ MB PDFs
	MaxPDFPagesExtract          = 30               // Maximum pages to extract from any regular PDF
	MaxCombinedTextChars        = 40000            // Maximum characters sent to AI (gpt-4o-mini supports 128k context)
	MaxYouTubeTranscriptChars   = 4000             // Per-video transcript cap — prevents crowding out other lesson content
)

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

	// Proactively validate and refresh OAuth token BEFORE starting batch job
	// This ensures we fail fast if tokens are invalid/expired rather than mid-processing
	_, err = s.userOAuthService.RefreshOAuthToken(ctx, &oauthCred)
	if err != nil {
		// Token refresh failed - credentials are invalid
		// User needs to re-authorize before generating catchups
		return nil, ErrOAuthTokenInvalid
	}
	fmt.Printf("OAuth token validated successfully for user %s\n", userOID.Hex())

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

	// Validate OAuth token before processing batch
	// This was already done when creating the job, but check again in case it expired
	_, err = s.userOAuthService.RefreshOAuthToken(ctx, &oauthCred)
	if err != nil {
		fmt.Printf("OAuth token refresh failed in batch processing: %v\n", err)
		s.failBatchJob(ctx, batchJobID, "oauth_invalid")
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
	for i, studentID := range studentIDs {
		// Re-fetch OAuth credentials periodically (every 5 students) to pick up any token refreshes
		// This ensures we always have the latest access token if it was refreshed during processing
		if i > 0 && i%5 == 0 {
			err := s.oauthCollection.FindOne(ctx, bson.M{
				"school_id": schoolID,
				"user_id":   teacherID,
			}).Decode(&oauthCred)
			if err != nil {
				fmt.Printf("Failed to re-fetch OAuth credentials: %v\n", err)
			} else {
				fmt.Printf("Re-fetched OAuth credentials (student %d/%d)\n", i+1, len(studentIDs))
			}
		}

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

	// Proactively validate and refresh OAuth token BEFORE processing
	// This ensures we fail fast if tokens are invalid/expired
	_, err = s.userOAuthService.RefreshOAuthToken(ctx, &oauthCred)
	if err != nil {
		// Token refresh failed - credentials are invalid
		return nil, ErrOAuthTokenInvalid
	}
	fmt.Printf("OAuth token validated successfully for user %s\n", userOID.Hex())

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

	if extractedContent.WordCount == 0 {
		s.failIngestionJob(processCtx, ingestionJob.ID, "no usable text content found in any classroom material")
		return ErrInsufficientContent
	}
	if !extractedContent.MeetsThreshold {
		// Log warning but proceed — the AI can still generate a useful lesson from limited content
		fmt.Printf("Content below threshold (%d words < %d required) but attempting generation for lesson\n",
			extractedContent.WordCount, MinWordCountThreshold)
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
	// Include if there's any content at all: description text OR any attachments (supported or not)
	// Unsupported attachments (YouTube, links) still contribute metadata/title as context
	included := len(strings.TrimSpace(cw.Description)) > 0 || len(attachments) > 0

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
		item.ExcludedNotes = append(item.ExcludedNotes, "No content found")
	}

	return item
}

func (s *CatchUpService) materialToContentItem(mat googleCourseMaterial, schoolID, courseID, ingestionJobID bson.ObjectID, now time.Time) models.ContentItem {
	attachments := s.parseMaterials(mat.Materials)
	// Include if there's any content at all: description text OR any attachments (supported or not)
	included := len(strings.TrimSpace(mat.Description)) > 0 || len(attachments) > 0

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
		item.ExcludedNotes = append(item.ExcludedNotes, "No content found")
	}

	return item
}

func (s *CatchUpService) announcementToContentItem(ann googleAnnouncement, schoolID, courseID, ingestionJobID bson.ObjectID, now time.Time) models.ContentItem {
	attachments := s.parseMaterials(ann.Materials)
	// Include if there is any text body OR any attachments (even unsupported ones like YouTube/links
	// provide title context for the AI to generate action items)
	included := len(strings.TrimSpace(ann.Text)) > 0 || len(attachments) > 0

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

	// Check Content-Length header first to avoid downloading huge files
	contentLength := resp.ContentLength
	if contentLength > int64(LargePDFMaxDownloadBytes) {
		fmt.Printf("Very large PDF (%.2f MB) — using reference marker without extraction\n",
			float64(contentLength)/(1024*1024))
		resp.Body.Close()
		return "[Large reference document — students should review this material as directed by their teacher]", nil
	}

	// Download the PDF (safe size or unknown size)
	pdfData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF data: %w", err)
	}

	// Double-check actual size in case Content-Length was wrong
	if len(pdfData) > LargePDFMaxDownloadBytes {
		fmt.Printf("Very large PDF (%.2f MB, reported as %.2f MB) — using reference marker\n",
			float64(len(pdfData))/(1024*1024), float64(contentLength)/(1024*1024))
		return "[Large reference document — students should review this material as directed by their teacher]", nil
	}

	// Files between 15 MB and 50 MB are large documents (e.g. textbooks) — sample evenly distributed pages.
	if len(pdfData) > LargePDFThresholdBytes {
		fmt.Printf("Large PDF detected (%.2f MB) — sampling %d evenly distributed pages\n",
			float64(len(pdfData))/(1024*1024), LargePDFSamplePageCount)
		sampled, sampErr := s.extractSampledPagesFromPDFBytes(pdfData)
		if sampErr == nil && sampled != "" {
			return sampled, nil
		}
		fmt.Printf("Page sampling failed: %v — falling back to reference marker\n", sampErr)
		return "[Large reference document — students should review this material as directed by their teacher]", nil
	}

	fmt.Printf("Downloaded PDF: %d bytes (%.2f MB)\n", len(pdfData), float64(len(pdfData))/(1024*1024))

	text, err := s.extractTextFromPDFBytes(pdfData, MaxPDFPagesExtract)
	if err == nil && len(strings.Fields(text)) >= 50 {
		return text, nil
	}

	pdfSizeMB := float64(len(pdfData)) / (1024 * 1024)
	wordCount := len(strings.Fields(text))

	fmt.Printf("Standard PDF extraction: %.2f MB, %d words extracted\n", pdfSizeMB, wordCount)

	if wordCount >= 50 {
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

func (s *CatchUpService) extractTextFromPDFBytes(pdfData []byte, pageLimit int) (string, error) {
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

	// Limit to the caller-specified page cap (default: MaxPDFPagesExtract = 30)
	maxPages := totalPages
	if maxPages > pageLimit {
		fmt.Printf("PDF has %d pages, limiting extraction to first %d pages\n", totalPages, pageLimit)
		maxPages = pageLimit
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

// extractSampledPagesFromPDFBytes extracts text from evenly distributed sample pages of a large PDF.
// Used for textbooks and reference documents where full extraction would be too slow or expensive.
func (s *CatchUpService) extractSampledPagesFromPDFBytes(pdfData []byte) (string, error) {
	tmpFile, err := os.CreateTemp("", "pdf-sample-*.pdf")
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

	totalPages := pdfReader.NumPage()
	if totalPages == 0 {
		return "", fmt.Errorf("PDF has no pages")
	}

	samplePages := selectSamplePages(totalPages, LargePDFSamplePageCount)
	fmt.Printf("Large PDF sampling: %d total pages, extracting pages %v\n", totalPages, samplePages)

	var textParts []string
	for _, pageNum := range samplePages {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}
		pageText, err := page.GetPlainText(nil)
		if err != nil || strings.TrimSpace(pageText) == "" {
			continue
		}
		textParts = append(textParts, fmt.Sprintf("[Page %d of %d]\n%s", pageNum, totalPages, pageText))
	}

	if len(textParts) == 0 {
		return "", fmt.Errorf("no text extracted from %d sampled pages", len(samplePages))
	}

	header := fmt.Sprintf("[Sampled %d pages from %d-page document — student should review full document]\n\n", len(textParts), totalPages)
	return header + strings.Join(textParts, "\n\n---\n\n"), nil
}

// extractRandomPagesFromPDFBytes extracts text from random pages of a very large PDF (50+ MB).
// Always includes first and last pages, fills remaining slots with random pages from throughout the document.
func (s *CatchUpService) extractRandomPagesFromPDFBytes(pdfData []byte, pageCount int) (string, error) {
	tmpFile, err := os.CreateTemp("", "pdf-random-*.pdf")
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

	totalPages := pdfReader.NumPage()
	if totalPages == 0 {
		return "", fmt.Errorf("PDF has no pages")
	}

	samplePages := selectRandomPages(totalPages, pageCount)
	fmt.Printf("Very large PDF extraction: %d total pages, extracting %d random pages: %v\n", totalPages, len(samplePages), samplePages)

	var textParts []string
	for _, pageNum := range samplePages {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}
		pageText, err := page.GetPlainText(nil)
		if err != nil || strings.TrimSpace(pageText) == "" {
			continue
		}
		textParts = append(textParts, fmt.Sprintf("[Page %d of %d]\n%s", pageNum, totalPages, pageText))
	}

	if len(textParts) == 0 {
		return "", fmt.Errorf("no text extracted from %d random pages", len(samplePages))
	}

	header := fmt.Sprintf("[Extracted %d random pages from %d-page document — student should review full document]\n\n", len(textParts), totalPages)
	return header + strings.Join(textParts, "\n\n---\n\n"), nil
}

// selectSamplePages returns up to sampleCount evenly distributed 1-based page numbers.
// Always includes the first page and distributes the rest across the full document.
func selectSamplePages(totalPages, sampleCount int) []int {
	if totalPages <= sampleCount {
		pages := make([]int, totalPages)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages
	}
	pages := make([]int, sampleCount)
	for i := 0; i < sampleCount; i++ {
		pages[i] = 1 + (i*(totalPages-1))/(sampleCount-1)
	}
	return pages
}

// selectRandomPages returns up to sampleCount random 1-based page numbers spread across the document.
// Always includes page 1 (first page) and the last page, then fills remaining slots with random pages.
func selectRandomPages(totalPages, sampleCount int) []int {
	if totalPages <= sampleCount {
		pages := make([]int, totalPages)
		for i := range pages {
			pages[i] = i + 1
		}
		return pages
	}

	pageSet := make(map[int]bool)
	pages := make([]int, 0, sampleCount)

	// Always include first and last pages
	pageSet[1] = true
	pageSet[totalPages] = true
	pages = append(pages, 1)

	// Fill remaining slots with random pages
	for len(pages) < sampleCount {
		randPage := rand.Intn(totalPages-2) + 2 // Pages 2 to totalPages-1
		if !pageSet[randPage] {
			pageSet[randPage] = true
			pages = append(pages, randPage)
		}
	}

	// Add last page if not already included
	if !pageSet[totalPages] {
		pages = append(pages, totalPages)
	}

	// Sort pages in ascending order using bubble sort
	for i := 0; i < len(pages)-1; i++ {
		for j := i + 1; j < len(pages); j++ {
			if pages[j] < pages[i] {
				pages[i], pages[j] = pages[j], pages[i]
			}
		}
	}

	return pages
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

	// OCR each image, capped at MaxPDFPagesExtract to prevent excessive processing
	maxPages := len(images)
	if maxPages > MaxPDFPagesExtract {
		fmt.Printf("Limiting OCR to first %d pages (PDF has %d pages)\n", MaxPDFPagesExtract, len(images))
		maxPages = MaxPDFPagesExtract
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

	// Limit pages for image conversion — capped at MaxPDFPagesExtract
	maxPages := numPages
	if maxPages > MaxPDFPagesExtract {
		maxPages = MaxPDFPagesExtract
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

// fetchYouTubeTranscript attempts to retrieve an English transcript for the given YouTube video ID.
// Order of methods:
//  1. Supadata API (cloud-safe, no IP blocking)
//  2. youtube-transcript-api Python library (works on non-cloud IPs)
//  3. Simple timedtext API fallback
//
// Returns (transcript, hasTranscript, error). A false hasTranscript means no captions are available.
func (s *CatchUpService) fetchYouTubeTranscript(ctx context.Context, videoID string) (string, bool, error) {
	if videoID == "" {
		return "", false, nil
	}

	fmt.Printf("[YouTube] Fetching transcript for video ID: %s\n", videoID)

	// Method 1: Supadata API — works on all servers including cloud/Linode IPs
	if s.config.SupadataAPIKey != "" {
		transcript, err := s.fetchTranscriptWithSupadata(ctx, videoID)
		if err == nil && transcript != "" {
			fmt.Printf("[YouTube] ✓ Supadata API success (video: %s, length: %d chars)\n", videoID, len(transcript))
			return transcript, true, nil
		}
		fmt.Printf("[YouTube] Supadata failed for %s: %v, trying next method...\n", videoID, err)
	} else {
		fmt.Printf("[YouTube] SUPADATA_API_KEY not set, skipping Supadata\n")
	}

	// Method 2: Python youtube-transcript-api (may be blocked on cloud IPs)
	transcript, hasTranscript, err := s.fetchTranscriptWithYtDlp(ctx, videoID)
	if err == nil && hasTranscript && transcript != "" {
		fmt.Printf("[YouTube] ✓ Python API success (video: %s, length: %d chars)\n", videoID, len(transcript))
		return transcript, true, nil
	}
	if err != nil {
		fmt.Printf("[YouTube] Python API failed for %s: %v\n", videoID, err)
	}

	// Method 3: Simple timedtext API
	transcript, hasTranscript, _ = s.trySimpleTranscriptFetch(ctx, videoID)
	if hasTranscript && transcript != "" {
		fmt.Printf("[YouTube] ✓ Simple API success (video: %s, length: %d chars)\n", videoID, len(transcript))
		return transcript, true, nil
	}

	fmt.Printf("[YouTube] ✗ No transcript available for video: %s\n", videoID)
	return "", false, nil
}

// fetchTranscriptWithSupadata uses the Supadata API to fetch YouTube transcripts.
// This works reliably on cloud/Linode servers where YouTube blocks direct requests.
func (s *CatchUpService) fetchTranscriptWithSupadata(ctx context.Context, videoID string) (string, error) {
	fmt.Printf("[YouTube Debug] Trying Supadata API for %s\n", videoID)

	apiURL := fmt.Sprintf("https://api.supadata.ai/v1/youtube/transcript?videoId=%s&text=true", videoID)

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return "", fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("x-api-key", s.config.SupadataAPIKey)

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Supadata returned status %d: %s", resp.StatusCode, string(body))
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response: %w", err)
	}

	var result struct {
		Content string `json:"content"`
		Error   string `json:"error"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return "", fmt.Errorf("failed to parse response: %w", err)
	}
	if result.Error != "" {
		return "", fmt.Errorf("Supadata error: %s", result.Error)
	}

	transcript := strings.TrimSpace(result.Content)
	if transcript == "" {
		return "", fmt.Errorf("Supadata returned empty transcript")
	}

	fmt.Printf("[YouTube Debug] Supadata success: %d chars\n", len(transcript))
	return transcript, nil
}

// isYtDlpAvailable checks if yt-dlp is installed on the system
func (s *CatchUpService) isYtDlpAvailable() bool {
	cmd := exec.Command("yt-dlp", "--version")
	err := cmd.Run()
	return err == nil
}

// fetchTranscriptWithYtDlp uses youtube-transcript-api (Python) to fetch subtitles,
// which is the most reliable method. Falls back to yt-dlp file download if unavailable.
func (s *CatchUpService) fetchTranscriptWithYtDlp(ctx context.Context, videoID string) (string, bool, error) {
	fmt.Printf("[YouTube Debug] Trying youtube-transcript-api (Python) for %s\n", videoID)

	// Use youtube-transcript-api Python library - most reliable method
	pythonScript := fmt.Sprintf(`
from youtube_transcript_api import YouTubeTranscriptApi
import sys, json
try:
    api = YouTubeTranscriptApi()
    transcript = api.fetch('%s')
    texts = [s.text for s in transcript.snippets]
    print(' '.join(texts))
except Exception as e:
    sys.stderr.write(str(e))
    sys.exit(1)
`, videoID)

	cmd := exec.CommandContext(ctx, "python3", "-c", pythonScript)
	output, err := cmd.Output()
	if err == nil && len(strings.TrimSpace(string(output))) > 0 {
		transcript := strings.TrimSpace(string(output))
		fmt.Printf("[YouTube Debug] youtube-transcript-api success: %d chars\n", len(transcript))
		return transcript, true, nil
	}

	if stderr, ok := err.(*exec.ExitError); ok {
		fmt.Printf("[YouTube Debug] youtube-transcript-api failed: %s\n", string(stderr.Stderr))
	} else if err != nil {
		fmt.Printf("[YouTube Debug] youtube-transcript-api error: %v\n", err)
	}

	return "", false, nil
}

// parseJSON3Subtitles parses YouTube's json3 subtitle format
func (s *CatchUpService) parseJSON3Subtitles(data []byte) (string, error) {
	// YouTube's json3 format structure
	type segment struct {
		Utf8 string `json:"utf8"`
	}
	type event struct {
		Segs []segment `json:"segs"`
	}
	type json3Format struct {
		Events []event `json:"events"`
	}

	var parsed json3Format
	if err := json.Unmarshal(data, &parsed); err != nil {
		return "", fmt.Errorf("JSON parsing failed: %w", err)
	}

	var textParts []string
	for _, evt := range parsed.Events {
		for _, seg := range evt.Segs {
			text := strings.TrimSpace(seg.Utf8)
			if text != "" && text != "\n" {
				textParts = append(textParts, text)
			}
		}
	}

	if len(textParts) == 0 {
		return "", nil
	}

	transcript := strings.Join(textParts, " ")

	// Clean up common subtitle artifacts
	transcript = strings.ReplaceAll(transcript, "  ", " ")
	transcript = strings.ReplaceAll(transcript, "\n\n", "\n")
	transcript = strings.TrimSpace(transcript)

	return transcript, nil
}

func (s *CatchUpService) trySimpleTranscriptFetch(ctx context.Context, videoID string) (string, bool, error) {
	transcriptURL := fmt.Sprintf(
		"https://www.youtube.com/api/timedtext?v=%s&lang=en",
		url.QueryEscape(videoID),
	)

	req, err := http.NewRequestWithContext(ctx, "GET", transcriptURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", false, nil
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", false, err
	}

	if len(strings.TrimSpace(string(body))) == 0 {
		return "", false, nil
	}

	return s.parseTranscriptXML(body)
}

func (s *CatchUpService) extractTranscriptFromVideoPage(ctx context.Context, videoID string) (string, bool, error) {
	videoPageURL := fmt.Sprintf("https://www.youtube.com/watch?v=%s", videoID)

	req, err := http.NewRequestWithContext(ctx, "GET", videoPageURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")
	req.Header.Set("Accept-Language", "en-US,en;q=0.9")

	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[YouTube Debug] Video page returned status %d for video %s\n", resp.StatusCode, videoID)
		return "", false, fmt.Errorf("video page returned status %d", resp.StatusCode)
	}

	// Read the HTML page
	body, err := io.ReadAll(io.LimitReader(resp.Body, 2*1024*1024)) // 2MB limit
	if err != nil {
		return "", false, err
	}

	// Extract the captionTracks JSON from the page
	// Look for: "captionTracks":[{"baseUrl":"...
	pageHTML := string(body)
	captionTracksIdx := strings.Index(pageHTML, `"captionTracks":`)
	if captionTracksIdx == -1 {
		fmt.Printf("[YouTube Debug] No captionTracks found in video page for %s\n", videoID)
		return "", false, nil
	}

	fmt.Printf("[YouTube Debug] Found captionTracks at index %d for video %s\n", captionTracksIdx, videoID)

	// Extract the baseUrl from the first caption track
	baseURLStart := strings.Index(pageHTML[captionTracksIdx:], `"baseUrl":"`)
	if baseURLStart == -1 {
		fmt.Printf("[YouTube Debug] No baseUrl found in captionTracks for video %s\n", videoID)
		return "", false, nil
	}
	baseURLStart += captionTracksIdx + len(`"baseUrl":"`)

	baseURLEnd := strings.Index(pageHTML[baseURLStart:], `"`)
	if baseURLEnd == -1 {
		fmt.Printf("[YouTube Debug] No closing quote for baseUrl for video %s\n", videoID)
		return "", false, nil
	}

	// The baseUrl is URL-escaped in the JSON, so we need to unescape it
	escapedURL := pageHTML[baseURLStart : baseURLStart+baseURLEnd]
	timedtextURL := strings.ReplaceAll(escapedURL, `\u0026`, "&")

	// Also handle other common escape sequences
	timedtextURL = strings.ReplaceAll(timedtextURL, `\/`, "/")
	timedtextURL = strings.ReplaceAll(timedtextURL, `\u003d`, "=")

	fmt.Printf("[YouTube Debug] Extracted timedtext URL (length: %d): %s\n", len(timedtextURL), timedtextURL)

	// Now fetch the transcript using the extracted URL
	transcript, hasTranscript, err := s.fetchTranscriptFromURL(ctx, timedtextURL)

	if err != nil {
		fmt.Printf("[YouTube Debug] fetchTranscriptFromURL error: %v\n", err)
	}
	if !hasTranscript {
		fmt.Printf("[YouTube Debug] fetchTranscriptFromURL returned hasTranscript=false\n")
	}
	if transcript == "" {
		fmt.Printf("[YouTube Debug] fetchTranscriptFromURL returned empty transcript\n")
	}

	return transcript, hasTranscript, err
}

func (s *CatchUpService) fetchTranscriptFromURL(ctx context.Context, timedtextURL string) (string, bool, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", timedtextURL, nil)
	if err != nil {
		return "", false, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		fmt.Printf("[YouTube Debug] HTTP request to timedtext URL failed: %v\n", err)
		return "", false, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		fmt.Printf("[YouTube Debug] Timedtext URL returned status %d\n", resp.StatusCode)
		bodyPreview, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		fmt.Printf("[YouTube Debug] Response preview: %s\n", string(bodyPreview))
		return "", false, fmt.Errorf("transcript fetch returned status %d", resp.StatusCode)
	}

	body, err := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	if err != nil {
		return "", false, err
	}

	fmt.Printf("[YouTube Debug] Fetched timedtext data, length: %d bytes\n", len(body))
	if len(body) > 0 {
		previewLen := 200
		if len(body) < previewLen {
			previewLen = len(body)
		}
		fmt.Printf("[YouTube Debug] First %d chars of response: %s\n", previewLen, string(body[:previewLen]))
	}

	return s.parseTranscriptXML(body)
}

func (s *CatchUpService) parseTranscriptXML(data []byte) (string, bool, error) {
	type textEntry struct {
		Text string `xml:",chardata"`
	}
	type transcriptXML struct {
		Texts []textEntry `xml:"text"`
	}

	var parsed transcriptXML
	if err := xml.Unmarshal(data, &parsed); err != nil {
		fmt.Printf("[YouTube Debug] XML parsing failed: %v\n", err)
		previewLen := 300
		if len(data) < previewLen {
			previewLen = len(data)
		}
		fmt.Printf("[YouTube Debug] Data to parse (first %d chars): %s\n", previewLen, string(data[:previewLen]))
		return "", false, fmt.Errorf("failed to parse transcript XML: %w", err)
	}

	fmt.Printf("[YouTube Debug] XML parsed successfully, found %d text entries\n", len(parsed.Texts))

	if len(parsed.Texts) == 0 {
		return "", false, nil
	}

	var parts []string
	for _, t := range parsed.Texts {
		text := strings.TrimSpace(t.Text)
		if text != "" {
			parts = append(parts, text)
		}
	}

	if len(parts) == 0 {
		fmt.Printf("[YouTube Debug] No non-empty text parts found after parsing\n")
		return "", false, nil
	}

	transcript := strings.Join(parts, " ")
	fmt.Printf("[YouTube Debug] Successfully extracted transcript: %d words\n", len(strings.Fields(transcript)))
	return transcript, true, nil
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
				switch att.Kind {
				case models.AttachmentKindVideo:
					// Attempt to fetch the YouTube transcript (15-second timeout per video)
					if att.ExternalID != "" {
						transcriptCtx, transcriptCancel := context.WithTimeout(ctx, 15*time.Second)
						transcript, hasTranscript, transcriptErr := s.fetchYouTubeTranscript(transcriptCtx, att.ExternalID)
						fmt.Printf("YouTube transcript result — hasTranscript: %v, transcript: %s, err: %v\n", hasTranscript, transcript, transcriptErr)
						transcriptCancel()
						if transcriptErr != nil {
							fmt.Printf("YouTube transcript error for '%s' (id=%s): %v\n", att.Title, att.ExternalID, transcriptErr)
						}
						if hasTranscript && transcript != "" {
							fmt.Printf("YouTube transcript fetched for '%s': %d words\n", att.Title, len(strings.Fields(transcript)))
							// Cap transcript length so a single long video doesn't crowd out other lesson content
							if len(transcript) > MaxYouTubeTranscriptChars {
								transcript = transcript[:MaxYouTubeTranscriptChars] + "... [transcript continues — key content captured above]"
								fmt.Printf("Transcript capped at %d chars\n", MaxYouTubeTranscriptChars)
							}
							combinedParts = append(combinedParts, fmt.Sprintf("[VIDEO_WITH_TRANSCRIPT: %s]\n%s", att.Title, transcript))
						} else {
							fmt.Printf("No transcript available for YouTube video '%s' — using title only\n", att.Title)
							combinedParts = append(combinedParts, fmt.Sprintf("[VIDEO_TITLE_ONLY: %s]", att.Title))
						}
					} else if att.Title != "" {
						combinedParts = append(combinedParts, fmt.Sprintf("[VIDEO_TITLE_ONLY: %s]", att.Title))
					}
				case models.AttachmentKindExternalURL:
					if att.Title != "" {
						combinedParts = append(combinedParts, fmt.Sprintf("[Reference material: %s]", att.Title))
					} else if att.URL != "" {
						combinedParts = append(combinedParts, fmt.Sprintf("[Reference: %s]", att.URL))
					}
				default:
					warnings = append(warnings, fmt.Sprintf("Unsupported attachment: %s (%s)", att.Title, att.Kind))
				}
				continue
			}

			// Add per-file timeout to prevent a single slow download from hanging the entire job.
			// PDFs (especially large textbooks) get a much longer timeout.
			attTimeoutSecs := ContentFetchTimeoutSecs
			if att.Kind == models.AttachmentKindPDF || att.MimeType == "application/pdf" || att.MimeType == "application/octet-stream" {
				attTimeoutSecs = LargePDFTimeoutSecs
			}
			attCtx, attCancel := context.WithTimeout(ctx, time.Duration(attTimeoutSecs)*time.Second)
			text, err := s.extractTextFromAttachment(attCtx, oauthCred, att)
			attCancel()
			if err != nil {
				fmt.Printf("Warning: failed to extract text from '%s': %v — skipping attachment\n", att.Title, err)
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

	// Truncate combined text to prevent token overflow with large documents
	contentText := extractedContent.CombinedText
	if len(contentText) > MaxCombinedTextChars {
		contentText = contentText[:MaxCombinedTextChars] +
			"\n\n[Content truncated — remaining material should be reviewed by the student directly]"
		fmt.Printf("Truncated content from %d to %d chars for AI generation\n",
			len(extractedContent.CombinedText), MaxCombinedTextChars)
	}

	// Debug: Show what content is being sent to AI
	videoCount := strings.Count(contentText, "[VIDEO_WITH_TRANSCRIPT:")
	videoOnlyCount := strings.Count(contentText, "[VIDEO_TITLE_ONLY:")
	refDocCount := strings.Count(contentText, "[Large reference document")
	fmt.Printf("Content breakdown for AI: %d videos w/ transcript, %d videos title-only, %d reference docs, %d total chars\n",
		videoCount, videoOnlyCount, refDocCount, len(contentText))

	client := openai.NewClient(s.config.OpenAIAPIKey)

	prompt := fmt.Sprintf(`You are an educational AI assistant helping create a catch-up lesson for a student who missed class.

Course: %s

Class content from the day the student missed:
%s

IMPORTANT — how to handle special content markers:
- "[VIDEO_WITH_TRANSCRIPT: <title>]" followed by transcript text: This is HIGH PRIORITY content. Use the transcript extensively to explain the video content as part of the lesson. Incorporate video concepts prominently into both the explanation and quiz sections. Videos often contain key learning material.
- "[VIDEO_TITLE_ONLY: <title>]" means no transcript was available. You MUST include this disclaimer in the explanation: "This summary is based on the video title only. Please watch the video for full understanding." Do NOT invent content from the title.
- "[Large reference document — ...]" means a large textbook or document was attached. Generate an action item like "Review the relevant chapter in the provided textbook" — do NOT try to summarise it.
For all other extracted text, summarise and explain the lesson content as normal.

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
   - PRIORITY: If video transcripts are available, prominently feature them with dedicated sections explaining the video content

3. Generate 5-7 learning objectives that the student should achieve after reviewing this content

4. Create a quiz with 5-7 questions to check understanding
   - Mix of multiple choice (4 options each) and short answer questions
   - Questions should test comprehension of the key concepts
   - Include the correct answer for each question
   - CRITICAL REQUIREMENT: If video transcripts are available, AT LEAST 1 question MUST be directly based on the video content/transcript. Do not skip this requirement.

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

IMPORTANT: The "explanation" field must contain valid HTML markup. Use semantic HTML tags to structure the content properly.`, course.Name, contentText)

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
		MaxTokens:   3000,
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
