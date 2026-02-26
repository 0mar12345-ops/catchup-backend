package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	ErrInvalidCatchUpCourseID  = errors.New("invalid course id")
	ErrInvalidCatchUpStudentID = errors.New("invalid student id")
	ErrNoContentFound          = errors.New("no content found for the specified date")
	ErrInsufficientContent     = errors.New("insufficient content to generate catch-up lesson")
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
	oauthCollection            *mongo.Collection
	config                     *config.Config
}

func NewCatchUpService(client *mongo.Client, dbName string, cfg *config.Config) *CatchUpService {
	db := client.Database(dbName)

	return &CatchUpService{
		coursesCollection:          db.Collection("courses"),
		studentsCollection:         db.Collection("students"),
		absenceRecordsCollection:   db.Collection("absence_records"),
		ingestionJobsCollection:    db.Collection("ingestion_jobs"),
		contentItemsCollection:     db.Collection("content_items"),
		extractedContentCollection: db.Collection("extracted_content"),
		catchUpLessonsCollection:   db.Collection("catchup_lessons"),
		oauthCollection:            db.Collection("oauth_credentials"),
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
}

func (s *CatchUpService) GenerateCatchUpForStudents(
	ctx context.Context,
	req GenerateCatchUpRequest,
	userID, schoolID string,
) (*GenerateCatchUpResult, error) {
	// Parse IDs
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

	// Parse absence date
	absenceDate, err := time.Parse("2006-01-02", req.AbsenceDate)
	if err != nil {
		return nil, errors.New("invalid date format, use YYYY-MM-DD")
	}

	// Verify course exists and belongs to teacher
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

	// Get OAuth credentials for the teacher
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

	// Process each student
	for _, studentIDStr := range req.StudentIDs {
		studentOID, err := bson.ObjectIDFromHex(studentIDStr)
		if err != nil {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Invalid student ID: %s", studentIDStr))
			continue
		}

		// Verify student exists
		count, err := s.studentsCollection.CountDocuments(ctx, bson.M{
			"_id":       studentOID,
			"school_id": schoolOID,
		})
		if err != nil || count == 0 {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Student not found: %s", studentIDStr))
			continue
		}

		// Process this student's catch-up
		err = s.processStudentCatchUp(ctx, schoolOID, courseOID, studentOID, userOID, absenceDate, &oauthCred, &course)
		if err != nil {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Failed for student %s: %v", studentIDStr, err))
			continue
		}

		result.SuccessCount++
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
	now := time.Now().UTC()

	// Create absence record
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

	_, err := s.absenceRecordsCollection.InsertOne(ctx, absenceRecord)
	if err != nil {
		return fmt.Errorf("failed to create absence record: %w", err)
	}

	// Create ingestion job
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

	_, err = s.ingestionJobsCollection.InsertOne(ctx, ingestionJob)
	if err != nil {
		return fmt.Errorf("failed to create ingestion job: %w", err)
	}

	// Update job to running
	startTime := now
	_, err = s.ingestionJobsCollection.UpdateOne(ctx,
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

	// Fetch classroom content for the date
	contentItems, err := s.fetchClassroomContent(ctx, oauthCred, course, absenceDate, schoolID, courseID, ingestionJob.ID)
	if err != nil {
		s.failIngestionJob(ctx, ingestionJob.ID, err.Error())
		return err
	}

	fmt.Print(contentItems)

	if len(contentItems) == 0 {
		s.failIngestionJob(ctx, ingestionJob.ID, "no content found for this date")
		return ErrNoContentFound
	}

	// Extract and combine text from content items
	extractedContent, err := s.extractAndCombineText(ctx, schoolID, courseID, absenceRecord.ID, ingestionJob.ID, contentItems)
	if err != nil {
		s.failIngestionJob(ctx, ingestionJob.ID, err.Error())
		return err
	}

	if !extractedContent.MeetsThreshold {
		s.failIngestionJob(ctx, ingestionJob.ID, "insufficient content - word count below threshold")
		return ErrInsufficientContent
	}

	// Create catch-up lesson (with status "empty" until AI generates content)
	catchUpLesson := models.CatchUpLesson{
		ID:                 bson.NewObjectID(),
		SchoolID:           schoolID,
		CourseID:           courseID,
		StudentID:          studentID,
		AbsenceRecordID:    absenceRecord.ID,
		ExtractedContentID: extractedContent.ID,
		Status:             models.CatchUpStatusEmpty,
		Explanation:        "", // Will be filled by AI
		Quiz:               []models.QuizQuestion{},
		RegenerationCount:  0,
		CreatedAt:          now,
		UpdatedAt:          now,
	}

	_, err = s.catchUpLessonsCollection.InsertOne(ctx, catchUpLesson)
	if err != nil {
		s.failIngestionJob(ctx, ingestionJob.ID, err.Error())
		return err
	}

	// Mark ingestion job as completed
	completedTime := time.Now().UTC()
	_, err = s.ingestionJobsCollection.UpdateOne(ctx,
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
	s.ingestionJobsCollection.UpdateOne(ctx,
		bson.M{"_id": jobID},
		bson.M{"$set": bson.M{
			"status":         models.IngestionJobFailed,
			"failure_reason": reason,
			"updated_at":     time.Now().UTC(),
		}},
	)
}

func (s *CatchUpService) fetchClassroomContent(
	ctx context.Context,
	oauthCred *models.OAuthCredential,
	course *models.Course,
	absenceDate time.Time,
	schoolID, courseID, ingestionJobID bson.ObjectID,
) ([]models.ContentItem, error) {
	// Get external course ID from course
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

	// Create OAuth2 token from credentials
	token := &oauth2.Token{
		AccessToken:  oauthCred.AccessTokenEnc,
		RefreshToken: oauthCred.RefreshTokenEnc,
		TokenType:    "Bearer",
	}
	if oauthCred.AccessTokenExpiry != nil {
		token.Expiry = *oauthCred.AccessTokenExpiry
	}

	// Create OAuth2 config
	oauthConfig := &oauth2.Config{
		ClientID:     s.config.GoogleClientID,
		ClientSecret: s.config.GoogleClientSecret,
		Endpoint:     google.Endpoint,
	}

	// Create HTTP client with auto-refresh
	client := oauthConfig.Client(ctx, token)

	now := time.Now().UTC()
	contentItems := []models.ContentItem{}

	// Fetch course work (assignments)
	courseWork, err := s.fetchCourseWork(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch coursework: %w", err)
	}

	// Convert coursework to content items
	for _, cw := range courseWork {
		item := s.courseWorkToContentItem(cw, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	// Fetch course work materials
	materials, err := s.fetchCourseMaterials(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch materials: %w", err)
	}

	// Convert materials to content items
	for _, mat := range materials {
		item := s.materialToContentItem(mat, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	// Fetch announcements
	announcements, err := s.fetchAnnouncements(ctx, client, externalCourseID, absenceDate)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch announcements: %w", err)
	}

	// Convert announcements to content items
	for _, ann := range announcements {
		item := s.announcementToContentItem(ann, schoolID, courseID, ingestionJobID, now)
		_, err := s.contentItemsCollection.InsertOne(ctx, item)
		if err == nil {
			contentItems = append(contentItems, item)
		}
	}

	return contentItems, nil
}

// Google Classroom API response structures
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

		// Filter by date
		for _, cw := range result.CourseWork {
			creationTime, err := time.Parse(time.RFC3339, cw.CreationTime)
			if err != nil {
				continue
			}

			// Check if created on the target date (same day)
			if isSameDay(creationTime, targetDate) {
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

		// Filter by date
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

		// Filter by date
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
			kind, isSupported := classifyDriveFile(df.MimeType)

			att := models.ContentAttachment{
				Title:       df.Title,
				URL:         fmt.Sprintf("https://drive.google.com/file/d/%s/view", df.ID),
				MimeType:    df.MimeType,
				Kind:        kind,
				IsSupported: isSupported,
				ExternalID:  df.ID,
			}

			if !isSupported {
				att.ExcludeCause = fmt.Sprintf("Unsupported file type: %s", df.MimeType)
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

func classifyDriveFile(mimeType string) (models.AttachmentKind, bool) {
	switch {
	case strings.Contains(mimeType, "google-apps.document"):
		return models.AttachmentKindGoogleDoc, true
	case strings.Contains(mimeType, "google-apps.presentation"):
		return models.AttachmentKindGoogleSlide, true
	case strings.Contains(mimeType, "pdf"):
		return models.AttachmentKindPDF, true
	case strings.Contains(mimeType, "image"):
		return models.AttachmentKindImage, false
	case strings.Contains(mimeType, "video"):
		return models.AttachmentKindVideo, false
	default:
		return models.AttachmentKindOther, false
	}
}

func hasTextContent(description string, attachments []models.ContentAttachment) bool {
	// Check if has description text
	if len(strings.TrimSpace(description)) > 50 {
		return true
	}

	// Check if has at least one supported attachment
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

func (s *CatchUpService) extractAndCombineText(
	ctx context.Context,
	schoolID, courseID, absenceRecordID, ingestionJobID bson.ObjectID,
	contentItems []models.ContentItem,
) (*models.ExtractedContent, error) {
	now := time.Now().UTC()

	// Combine all text from included content items
	var combinedParts []string
	var contentItemIDs []bson.ObjectID
	warnings := []string{}

	for _, item := range contentItems {
		if !item.Included {
			continue
		}

		contentItemIDs = append(contentItemIDs, item.ID)

		// Add title and description
		if item.Title != "" {
			combinedParts = append(combinedParts, item.Title)
		}
		if item.Description != "" {
			combinedParts = append(combinedParts, item.Description)
		}

		// TODO: Extract text from attachments (Google Docs, Slides, PDFs)
		// For now, just note supported attachments
		for _, att := range item.Attachments {
			if !att.IsSupported {
				warnings = append(warnings, fmt.Sprintf("Unsupported attachment: %s (%s)", att.Title, att.Kind))
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
