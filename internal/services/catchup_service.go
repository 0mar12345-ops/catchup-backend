package services

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/0mar12345-ops/config"
	"github.com/0mar12345-ops/internal/models"
	"github.com/ledongthuc/pdf"
	openai "github.com/sashabaranov/go-openai"
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

	for _, studentIDStr := range req.StudentIDs {
		studentOID, err := bson.ObjectIDFromHex(studentIDStr)
		if err != nil {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Invalid student ID: %s", studentIDStr))
			continue
		}

		count, err := s.studentsCollection.CountDocuments(ctx, bson.M{
			"_id":       studentOID,
			"school_id": schoolOID,
		})
		if err != nil || count == 0 {
			result.FailedCount++
			result.Warnings = append(result.Warnings, fmt.Sprintf("Student not found: %s", studentIDStr))
			continue
		}

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

	extractedContent, err := s.extractAndCombineText(ctx, schoolID, courseID, absenceRecord.ID, ingestionJob.ID, contentItems, oauthCred)
	if err != nil {
		s.failIngestionJob(ctx, ingestionJob.ID, err.Error())
		return err
	}

	if !extractedContent.MeetsThreshold {
		s.failIngestionJob(ctx, ingestionJob.ID, "insufficient content - word count below threshold")
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
			kind, isSupported := classifyDriveFile(df.MimeType, df.Title)

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

func classifyDriveFile(mimeType, fileName string) (models.AttachmentKind, bool) {
	mimeType = strings.ToLower(mimeType)
	fileName = strings.ToLower(fileName)

	switch {
	case strings.Contains(mimeType, "google-apps.document"):
		return models.AttachmentKindGoogleDoc, true
	case strings.Contains(mimeType, "google-apps.presentation"):
		return models.AttachmentKindGoogleSlide, true
	case strings.Contains(mimeType, "pdf") || mimeType == "application/pdf":
		return models.AttachmentKindPDF, true
	case strings.Contains(mimeType, "image"):
		return models.AttachmentKindImage, false
	case strings.Contains(mimeType, "video"):
		return models.AttachmentKindVideo, false
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

func (s *CatchUpService) createOAuthClient(ctx context.Context, oauthCred *models.OAuthCredential) (*http.Client, error) {

	if oauthCred.RefreshTokenEnc == "" {
		return nil, errors.New("refresh token not found - user needs to re-authorize")
	}

	token := &oauth2.Token{
		AccessToken:  oauthCred.AccessTokenEnc,
		RefreshToken: oauthCred.RefreshTokenEnc,
		TokenType:    "Bearer",
	}
	if oauthCred.AccessTokenExpiry != nil {
		token.Expiry = *oauthCred.AccessTokenExpiry
	}

	oauthConfig := &oauth2.Config{
		ClientID:     s.config.GoogleClientID,
		ClientSecret: s.config.GoogleClientSecret,
		Endpoint:     google.Endpoint,
		Scopes: []string{
			"https://www.googleapis.com/auth/classroom.courses.readonly",
			"https://www.googleapis.com/auth/classroom.coursework.students.readonly",
			"https://www.googleapis.com/auth/classroom.courseworkmaterials.readonly",
			"https://www.googleapis.com/auth/classroom.announcements.readonly",
			"https://www.googleapis.com/auth/drive.readonly",
		},
	}

	return oauthConfig.Client(ctx, token), nil
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

	switch att.Kind {
	case models.AttachmentKindGoogleDoc:
		return s.extractFromGoogleDoc(ctx, client, att.ExternalID)
	case models.AttachmentKindGoogleSlide:
		return s.extractFromGoogleSlides(ctx, client, att.ExternalID)
	case models.AttachmentKindPDF:
		return s.extractFromPDF(ctx, client, att.ExternalID)
	default:
		return "", fmt.Errorf("unsupported attachment kind: %s", att.Kind)
	}
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

	pdfData, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read PDF data: %w", err)
	}

	text, err := s.extractTextFromPDFBytes(pdfData)
	if err == nil && len(strings.Fields(text)) >= 50 {
		return text, nil
	}

	if s.config.OpenAIAPIKey != "" {
		aiText, aiErr := s.extractWithOpenAIVision(ctx, pdfData, fileID)
		if aiErr == nil && aiText != "" {
			return aiText, nil
		}

		if aiErr != nil {
			fmt.Printf("OpenAI Vision fallback failed: %v\n", aiErr)
		}
	}

	if text != "" {
		return text, nil
	}

	return "", fmt.Errorf("PDF text extraction failed: library extraction yielded no text and OpenAI fallback unavailable or failed")
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

	for pageNum := 1; pageNum <= totalPages; pageNum++ {
		page := pdfReader.Page(pageNum)
		if page.V.IsNull() {
			continue
		}

		pageText, err := page.GetPlainText(nil)
		if err != nil {
			continue
		}

		if pageText != "" {
			textParts = append(textParts, pageText)
		}
	}

	return strings.Join(textParts, "\n\n"), nil
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
