package services

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
	"go.mongodb.org/mongo-driver/v2/mongo/options"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

var (
	ErrInvalidOAuthState   = errors.New("invalid oauth state")
	ErrMissingOAuthCode    = errors.New("missing oauth code")
	ErrOAuthSchoolNotFound = errors.New("no school found in database")
	ErrInvalidAuthUserID   = errors.New("invalid auth user id")
	ErrInvalidAuthSchoolID = errors.New("invalid auth school id")
)

type UserOAuthService struct {
	config      *oauth2.Config
	state       string
	frontendURL string

	schoolsCollection     *mongo.Collection
	usersCollection       *mongo.Collection
	oauthCollection       *mongo.Collection
	coursesCollection     *mongo.Collection
	studentsCollection    *mongo.Collection
	enrollmentsCollection *mongo.Collection
}

func NewUserOAuthService(
	clientID, clientSecret, redirectURL, state, frontendURL string,
	client *mongo.Client,
	dbName string,
) *UserOAuthService {
	db := client.Database(dbName)

	return &UserOAuthService{
		config: &oauth2.Config{
			ClientID:     clientID,
			ClientSecret: clientSecret,
			RedirectURL:  redirectURL,
			Endpoint:     google.Endpoint,
			Scopes: []string{
				"openid",
				"https://www.googleapis.com/auth/userinfo.email",
				"https://www.googleapis.com/auth/userinfo.profile",
				"https://www.googleapis.com/auth/classroom.courses.readonly",
				"https://www.googleapis.com/auth/classroom.rosters.readonly",
				"https://www.googleapis.com/auth/classroom.coursework.students.readonly",
				"https://www.googleapis.com/auth/classroom.courseworkmaterials.readonly",
				"https://www.googleapis.com/auth/classroom.announcements.readonly",
				"https://www.googleapis.com/auth/drive.readonly",
			},
		},
		state:       state,
		frontendURL: frontendURL,

		schoolsCollection:     db.Collection("schools"),
		usersCollection:       db.Collection("users"),
		oauthCollection:       db.Collection("oauth_credentials"),
		coursesCollection:     db.Collection("courses"),
		studentsCollection:    db.Collection("students"),
		enrollmentsCollection: db.Collection("enrollments"),
	}
}

func (s *UserOAuthService) GetGoogleAuthURL() string {
	return s.config.AuthCodeURL(s.state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
}

func (s *UserOAuthService) GetGoogleAuthURLWithPrompt(forceConsent bool) string {
	if forceConsent {
		return s.config.AuthCodeURL(s.state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)
	}

	return s.config.AuthCodeURL(
		s.state,
		oauth2.AccessTypeOffline,
		oauth2.SetAuthURLParam("prompt", "select_account"),
	)
}

func (s *UserOAuthService) FrontendURL() string {
	return s.frontendURL
}

func (s *UserOAuthService) CheckUserExistsByEmail(ctx context.Context, email string) (bool, error) {
	count, err := s.usersCollection.CountDocuments(ctx, bson.M{"email": email})
	if err != nil {
		return false, err
	}
	return count > 0, nil
}

type MeData struct {
	User models.User `json:"user"`
}

func (s *UserOAuthService) GetMeData(ctx context.Context, userID, schoolID string) (*MeData, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidAuthUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidAuthSchoolID
	}

	var user models.User
	if err := s.usersCollection.FindOne(ctx, bson.M{"_id": userOID, "school_id": schoolOID}).Decode(&user); err != nil {
		return nil, err
	}

	return &MeData{
		User: user,
	}, nil
}

type OAuthSyncResult struct {
	TeacherEmail       string `json:"teacher_email"`
	TeacherName        string `json:"teacher_name"`
	SchoolID           string `json:"school_id"`
	UserID             string `json:"user_id"`
	CoursesSynced      int    `json:"courses_synced"`
	StudentsSynced     int    `json:"students_synced"`
	EnrollmentsSynced  int    `json:"enrollments_synced"`
	GrantedScopesCount int    `json:"granted_scopes_count"`
}

func (s *UserOAuthService) HandleGoogleCallback(ctx context.Context, state, code string) (*OAuthSyncResult, error) {
	if state != s.state {
		return nil, ErrInvalidOAuthState
	}

	if strings.TrimSpace(code) == "" {
		return nil, ErrMissingOAuthCode
	}

	token, err := s.config.Exchange(ctx, code)
	if err != nil {
		return nil, err
	}

	client := s.config.Client(ctx, token)

	userInfo, err := s.fetchGoogleUserInfo(ctx, client)
	if err != nil {
		return nil, err
	}

	courses, err := s.fetchAllCourses(ctx, client)
	if err != nil {
		return nil, err
	}

	grantedScopes := []string{}
	if scopeRaw, ok := token.Extra("scope").(string); ok && scopeRaw != "" {
		grantedScopes = strings.Split(scopeRaw, " ")
	}

	now := time.Now().UTC()

	school, err := s.getFirstSchool(ctx)
	if err != nil {
		return nil, err
	}

	teacher, err := s.upsertTeacher(ctx, school.ID, userInfo, now)
	if err != nil {
		return nil, err
	}

	if err := s.upsertOAuthCredential(ctx, school.ID, teacher.ID, token, grantedScopes, now); err != nil {
		return nil, err
	}

	studentSet := map[string]struct{}{}
	coursesSynced := 0
	enrollmentsSynced := 0

	for _, gc := range courses {
		course, err := s.upsertCourse(ctx, school.ID, teacher.ID, gc, now)
		if err != nil {
			continue
		}
		coursesSynced++

		courseStudents, err := s.fetchAllCourseStudents(ctx, client, gc.ID)
		if err != nil {
			continue
		}

		for _, gs := range courseStudents {
			student, err := s.upsertStudent(ctx, school.ID, gs, now)
			if err != nil {
				continue
			}

			if err := s.upsertEnrollment(ctx, school.ID, course.ID, student.ID, now); err == nil {
				enrollmentsSynced++
			}

			if gs.UserID != "" {
				studentSet[gs.UserID] = struct{}{}
			}
		}
	}

	return &OAuthSyncResult{
		TeacherEmail:       userInfo.Email,
		TeacherName:        userInfo.Name,
		SchoolID:           school.ID.Hex(),
		UserID:             teacher.ID.Hex(),
		CoursesSynced:      coursesSynced,
		StudentsSynced:     len(studentSet),
		EnrollmentsSynced:  enrollmentsSynced,
		GrantedScopesCount: len(grantedScopes),
	}, nil
}

type googleUserInfo struct {
	ID            string `json:"id"`
	Email         string `json:"email"`
	Name          string `json:"name"`
	VerifiedEmail bool   `json:"verified_email"`
	HD            string `json:"hd"`
}

type googleCoursesResponse struct {
	Courses       []googleCourse `json:"courses"`
	NextPageToken string         `json:"nextPageToken"`
}

type googleCourse struct {
	ID                 string `json:"id"`
	Name               string `json:"name"`
	Section            string `json:"section"`
	DescriptionHeading string `json:"descriptionHeading"`
	Description        string `json:"description"`
	Room               string `json:"room"`
	CourseState        string `json:"courseState"`
}

type googleStudentsResponse struct {
	Students      []googleStudent `json:"students"`
	NextPageToken string          `json:"nextPageToken"`
}

type googleStudent struct {
	UserID  string `json:"userId"`
	Profile struct {
		Name struct {
			FullName string `json:"fullName"`
		} `json:"name"`
		EmailAddress string `json:"emailAddress"`
	} `json:"profile"`
}

func (s *UserOAuthService) fetchGoogleUserInfo(ctx context.Context, client *http.Client) (*googleUserInfo, error) {
	body, err := s.fetchRaw(ctx, client, "https://www.googleapis.com/oauth2/v2/userinfo")
	if err != nil {
		return nil, err
	}

	var info googleUserInfo
	if err := json.Unmarshal(body, &info); err != nil {
		return nil, err
	}

	return &info, nil
}

func (s *UserOAuthService) fetchAllCourses(ctx context.Context, client *http.Client) ([]googleCourse, error) {
	allCourses := make([]googleCourse, 0)
	pageToken := ""

	for {
		url := "https://classroom.googleapis.com/v1/courses?pageSize=100"
		if pageToken != "" {
			url += "&pageToken=" + pageToken
		}

		body, err := s.fetchRaw(ctx, client, url)
		if err != nil {
			return nil, err
		}

		var response googleCoursesResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}

		allCourses = append(allCourses, response.Courses...)
		if response.NextPageToken == "" {
			break
		}

		pageToken = response.NextPageToken
	}

	return allCourses, nil
}

func (s *UserOAuthService) fetchAllCourseStudents(ctx context.Context, client *http.Client, courseID string) ([]googleStudent, error) {
	allStudents := make([]googleStudent, 0)
	pageToken := ""

	for {
		url := fmt.Sprintf("https://classroom.googleapis.com/v1/courses/%s/students?pageSize=100", courseID)
		if pageToken != "" {
			url += "&pageToken=" + pageToken
		}

		body, err := s.fetchRaw(ctx, client, url)
		if err != nil {
			if strings.Contains(err.Error(), "Requested entity was not found") || strings.Contains(err.Error(), "PERMISSION_DENIED") {
				return allStudents, nil
			}
			return nil, err
		}

		var response googleStudentsResponse
		if err := json.Unmarshal(body, &response); err != nil {
			return nil, err
		}

		allStudents = append(allStudents, response.Students...)
		if response.NextPageToken == "" {
			break
		}

		pageToken = response.NextPageToken
	}

	return allStudents, nil
}

func (s *UserOAuthService) fetchRaw(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return []byte{}, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return []byte{}, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return []byte{}, err
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return []byte{}, fmt.Errorf("google api request failed: %s", strings.TrimSpace(string(body)))
	}

	return body, nil
}

func (s *UserOAuthService) getFirstSchool(ctx context.Context) (*models.School, error) {
	var school models.School
	err := s.schoolsCollection.FindOne(
		ctx,
		bson.M{},
		options.FindOne().SetSort(bson.D{{Key: "created_at", Value: 1}}),
	).Decode(&school)
	if errors.Is(err, mongo.ErrNoDocuments) {
		return nil, ErrOAuthSchoolNotFound
	}
	if err != nil {
		return nil, err
	}

	return &school, nil
}

func (s *UserOAuthService) upsertTeacher(ctx context.Context, schoolID bson.ObjectID, info *googleUserInfo, now time.Time) (*models.User, error) {
	normalizedEmail := strings.TrimSpace(strings.ToLower(info.Email))
	filter := bson.M{
		"school_id": schoolID,
		"$or": []bson.M{
			{"google_user_id": info.ID},
			{"email": normalizedEmail},
		},
	}

	user := models.User{
		SchoolID:     schoolID,
		Role:         models.UserRoleTeacher,
		Name:         info.Name,
		Email:        normalizedEmail,
		GoogleUserID: info.ID,
		IsActive:     true,
		LastLoginAt:  &now,
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:     models.ProviderGoogleOAuth,
			ExternalID:   info.ID,
			LastSyncedAt: &now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	var existing models.User
	err := s.usersCollection.FindOne(ctx, filter).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		user.ID = bson.NewObjectID()
		_, err := s.usersCollection.InsertOne(ctx, user)
		if err != nil {
			return nil, err
		}
		return &user, nil
	}
	if err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{
		"name":           user.Name,
		"email":          user.Email,
		"google_user_id": user.GoogleUserID,
		"role":           user.Role,
		"is_active":      true,
		"last_login_at":  &now,
		"external_refs":  user.ExternalRefs,
		"updated_at":     now,
	}}

	_, err = s.usersCollection.UpdateOne(ctx, bson.M{"_id": existing.ID}, update)
	if err != nil {
		return nil, err
	}

	existing.Name = user.Name
	existing.Email = user.Email
	existing.Role = user.Role
	existing.IsActive = true
	existing.LastLoginAt = &now
	existing.ExternalRefs = user.ExternalRefs
	existing.UpdatedAt = now

	return &existing, nil
}

func (s *UserOAuthService) upsertOAuthCredential(
	ctx context.Context,
	schoolID, userID bson.ObjectID,
	token *oauth2.Token,
	grantedScopes []string,
	now time.Time,
) error {
	filter := bson.M{
		"school_id": schoolID,
		"user_id":   userID,
		"provider":  string(models.ProviderGoogleOAuth),
	}

	update := bson.M{
		"$set": bson.M{
			"scopes":              grantedScopes,
			"refresh_token_enc":   token.RefreshToken,
			"access_token_enc":    token.AccessToken,
			"access_token_expiry": token.Expiry,
			"granted_at":          now,
			"updated_at":          now,
		},
		"$setOnInsert": bson.M{
			"_id":        bson.NewObjectID(),
			"school_id":  schoolID,
			"user_id":    userID,
			"provider":   string(models.ProviderGoogleOAuth),
			"created_at": now,
		},
	}

	_, err := s.oauthCollection.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}

func (s *UserOAuthService) upsertCourse(
	ctx context.Context,
	schoolID, teacherID bson.ObjectID,
	gc googleCourse,
	now time.Time,
) (*models.Course, error) {
	filter := bson.M{
		"school_id": schoolID,
		"external_refs": bson.M{
			"$elemMatch": bson.M{
				"provider":    models.ProviderGoogleClassroom,
				"external_id": gc.ID,
			},
		},
	}

	course := models.Course{
		SchoolID:     schoolID,
		TeacherID:    teacherID,
		Name:         gc.Name,
		Section:      gc.Section,
		Subject:      gc.DescriptionHeading,
		Room:         gc.Room,
		StudentCount: 0,
		Source:       models.CourseSourceGoogleClassroom,
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:     models.ProviderGoogleClassroom,
			ExternalID:   gc.ID,
			LastSyncedAt: &now,
		}},
		IsArchived:   strings.EqualFold(gc.CourseState, "ARCHIVED"),
		LastSyncedAt: &now,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	var existing models.Course
	err := s.coursesCollection.FindOne(ctx, filter).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		course.ID = bson.NewObjectID()
		_, err := s.coursesCollection.InsertOne(ctx, course)
		if err != nil {
			return nil, err
		}
		return &course, nil
	}
	if err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{
		"teacher_id":     course.TeacherID,
		"name":           course.Name,
		"section":        course.Section,
		"subject":        course.Subject,
		"room":           course.Room,
		"source":         course.Source,
		"external_refs":  course.ExternalRefs,
		"is_archived":    course.IsArchived,
		"last_synced_at": course.LastSyncedAt,
		"updated_at":     now,
	}}

	_, err = s.coursesCollection.UpdateOne(ctx, bson.M{"_id": existing.ID}, update)
	if err != nil {
		return nil, err
	}

	existing.TeacherID = course.TeacherID
	existing.Name = course.Name
	existing.Section = course.Section
	existing.Subject = course.Subject
	existing.Room = course.Room
	existing.Source = course.Source
	existing.ExternalRefs = course.ExternalRefs
	existing.IsArchived = course.IsArchived
	existing.LastSyncedAt = course.LastSyncedAt
	existing.UpdatedAt = now

	return &existing, nil
}

func (s *UserOAuthService) upsertStudent(ctx context.Context, schoolID bson.ObjectID, gs googleStudent, now time.Time) (*models.Student, error) {
	filter := bson.M{
		"school_id": schoolID,
		"external_refs": bson.M{
			"$elemMatch": bson.M{
				"provider":    models.ProviderGoogleClassroom,
				"external_id": gs.UserID,
			},
		},
	}

	student := models.Student{
		SchoolID: schoolID,
		Name:     gs.Profile.Name.FullName,
		Email:    gs.Profile.EmailAddress,
		IsActive: true,
		ExternalRefs: []models.ExternalSystemRef{{
			Provider:     models.ProviderGoogleClassroom,
			ExternalID:   gs.UserID,
			LastSyncedAt: &now,
		}},
		CreatedAt: now,
		UpdatedAt: now,
	}

	var existing models.Student
	err := s.studentsCollection.FindOne(ctx, filter).Decode(&existing)
	if errors.Is(err, mongo.ErrNoDocuments) {
		student.ID = bson.NewObjectID()
		_, err := s.studentsCollection.InsertOne(ctx, student)
		if err != nil {
			return nil, err
		}
		return &student, nil
	}
	if err != nil {
		return nil, err
	}

	update := bson.M{"$set": bson.M{
		"name":          student.Name,
		"email":         student.Email,
		"is_active":     true,
		"external_refs": student.ExternalRefs,
		"updated_at":    now,
	}}

	_, err = s.studentsCollection.UpdateOne(ctx, bson.M{"_id": existing.ID}, update)
	if err != nil {
		return nil, err
	}

	existing.Name = student.Name
	existing.Email = student.Email
	existing.IsActive = true
	existing.ExternalRefs = student.ExternalRefs
	existing.UpdatedAt = now

	return &existing, nil
}

func (s *UserOAuthService) upsertEnrollment(
	ctx context.Context,
	schoolID, courseID, studentID bson.ObjectID,
	now time.Time,
) error {
	filter := bson.M{
		"school_id":  schoolID,
		"course_id":  courseID,
		"student_id": studentID,
	}

	update := bson.M{
		"$set": bson.M{
			"status":     models.EnrollmentActive,
			"updated_at": now,
		},
		"$setOnInsert": bson.M{
			"_id":           bson.NewObjectID(),
			"school_id":     schoolID,
			"course_id":     courseID,
			"student_id":    studentID,
			"created_at":    now,
			"external_refs": []models.ExternalSystemRef{},
		},
	}

	_, err := s.enrollmentsCollection.UpdateOne(ctx, filter, update, options.UpdateOne().SetUpsert(true))
	return err
}
