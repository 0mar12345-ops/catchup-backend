package services

import (
	"context"
	"errors"
	"fmt"
	"time"

	"go.mongodb.org/mongo-driver/v2/bson"
	"golang.org/x/oauth2"
)

type CoursePreview struct {
	GoogleClassroomID string `json:"google_classroom_id"`
	Name              string `json:"name"`
	Section           string `json:"section"`
}

// GetAvailableCourses fetches the teacher's Google Classroom courses without writing anything to the database.
func (s *UserOAuthService) GetAvailableCourses(ctx context.Context, userID, schoolID string) ([]CoursePreview, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidAuthUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidAuthSchoolID
	}

	oauthCred, err := s.GetOAuthCredential(ctx, userOID, schoolOID)
	if err != nil {
		return nil, errors.New("oauth credentials not found - please connect Google Classroom first")
	}

	token := &oauth2.Token{
		AccessToken:  oauthCred.AccessTokenEnc,
		RefreshToken: oauthCred.RefreshTokenEnc,
		TokenType:    "Bearer",
	}
	if oauthCred.AccessTokenExpiry != nil {
		token.Expiry = *oauthCred.AccessTokenExpiry
	}

	client := s.config.Client(ctx, token)

	courses, err := s.fetchAllCourses(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch courses: %w", err)
	}

	previews := make([]CoursePreview, 0, len(courses))
	for _, gc := range courses {
		previews = append(previews, CoursePreview{
			GoogleClassroomID: gc.ID,
			Name:              gc.Name,
			Section:           gc.Section,
		})
	}

	return previews, nil
}

// ImportSelectedCourses imports only the Google Classroom courses whose IDs are in classroomIDs,
// including their students and enrollments.
func (s *UserOAuthService) ImportSelectedCourses(ctx context.Context, userID, schoolID string, classroomIDs []string) (*OAuthSyncResult, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return nil, ErrInvalidAuthUserID
	}

	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return nil, ErrInvalidAuthSchoolID
	}

	oauthCred, err := s.GetOAuthCredential(ctx, userOID, schoolOID)
	if err != nil {
		return nil, errors.New("oauth credentials not found - please connect Google Classroom first")
	}

	token := &oauth2.Token{
		AccessToken:  oauthCred.AccessTokenEnc,
		RefreshToken: oauthCred.RefreshTokenEnc,
		TokenType:    "Bearer",
	}
	if oauthCred.AccessTokenExpiry != nil {
		token.Expiry = *oauthCred.AccessTokenExpiry
	}

	client := s.config.Client(ctx, token)

	allCourses, err := s.fetchAllCourses(ctx, client)
	if err != nil {
		return nil, fmt.Errorf("failed to fetch courses: %w", err)
	}

	wanted := make(map[string]struct{}, len(classroomIDs))
	for _, id := range classroomIDs {
		wanted[id] = struct{}{}
	}

	now := time.Now().UTC()
	studentSet := map[string]struct{}{}
	coursesSynced := 0
	enrollmentsSynced := 0

	for _, gc := range allCourses {
		if _, ok := wanted[gc.ID]; !ok {
			continue
		}

		course, err := s.upsertCourse(ctx, schoolOID, userOID, gc, now)
		if err != nil {
			continue
		}
		coursesSynced++

		courseStudents, err := s.fetchAllCourseStudents(ctx, client, gc.ID)
		if err != nil {
			continue
		}

		for _, gs := range courseStudents {
			student, err := s.upsertStudent(ctx, schoolOID, gs, now)
			if err != nil {
				continue
			}

			if err := s.upsertEnrollment(ctx, schoolOID, course.ID, student.ID, now); err == nil {
				enrollmentsSynced++
			}

			if gs.UserID != "" {
				studentSet[gs.UserID] = struct{}{}
			}
		}
	}

	return &OAuthSyncResult{
		SchoolID:          schoolOID.Hex(),
		UserID:            userOID.Hex(),
		CoursesSynced:     coursesSynced,
		StudentsSynced:    len(studentSet),
		EnrollmentsSynced: enrollmentsSynced,
	}, nil
}
