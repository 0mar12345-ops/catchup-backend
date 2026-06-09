package services

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/0mar12345-ops/internal/models"
	"go.mongodb.org/mongo-driver/v2/bson"
	"go.mongodb.org/mongo-driver/v2/mongo"
)

type RoleService struct {
	usersCollection  *mongo.Collection
	userOAuthService *UserOAuthService
}

func NewRoleService(client *mongo.Client, dbName string, userOAuthService *UserOAuthService) *RoleService {
	return &RoleService{
		usersCollection:  client.Database(dbName).Collection("users"),
		userOAuthService: userOAuthService,
	}
}

// UpdateUserRole sets the role of any user within the caller's school.
// Admin-only authorization is enforced at the route level via RequireRole.
func (s *RoleService) UpdateUserRole(ctx context.Context, callerSchoolID, targetUserID, newRole string) error {
	schoolOID, err := bson.ObjectIDFromHex(callerSchoolID)
	if err != nil {
		return fmt.Errorf("invalid school id")
	}
	targetOID, err := bson.ObjectIDFromHex(targetUserID)
	if err != nil {
		return fmt.Errorf("invalid user id")
	}

	role := models.UserRole(newRole)
	if role != models.UserRoleTeacher && role != models.UserRoleStudent && role != models.UserRoleAdmin {
		return fmt.Errorf("invalid role: must be teacher, student, or admin")
	}

	res, err := s.usersCollection.UpdateOne(ctx,
		bson.M{"_id": targetOID, "school_id": schoolOID},
		bson.M{"$set": bson.M{"role": role}},
	)
	if err != nil {
		return err
	}
	if res.MatchedCount == 0 {
		return fmt.Errorf("user not found in school")
	}
	return nil
}

// DetectAndUpdateRole calls the Google Classroom API to determine whether the user
// teaches any courses, then updates their stored role accordingly.
// Admin role is never downgraded by this method.
func (s *RoleService) DetectAndUpdateRole(ctx context.Context, userID, schoolID string) (string, error) {
	userOID, err := bson.ObjectIDFromHex(userID)
	if err != nil {
		return "", fmt.Errorf("invalid user id")
	}
	schoolOID, err := bson.ObjectIDFromHex(schoolID)
	if err != nil {
		return "", fmt.Errorf("invalid school id")
	}

	var user models.User
	if err := s.usersCollection.FindOne(ctx, bson.M{"_id": userOID, "school_id": schoolOID}).Decode(&user); err != nil {
		return "", fmt.Errorf("user not found")
	}
	if user.Role == models.UserRoleAdmin {
		return string(models.UserRoleAdmin), nil
	}

	cred, err := s.userOAuthService.GetOAuthCredential(ctx, userOID, schoolOID)
	if err != nil {
		return "", fmt.Errorf("no google account connected – please authorise in Settings")
	}
	httpClient, err := s.userOAuthService.RefreshOAuthToken(ctx, cred)
	if err != nil {
		return "", fmt.Errorf("google account needs re-authorisation")
	}

	hasCourses, err := s.hasCourses(ctx, httpClient)
	if err != nil {
		return "", fmt.Errorf("failed to check Google Classroom: %w", err)
	}

	var newRole models.UserRole
	if hasCourses {
		newRole = models.UserRoleTeacher
	} else {
		newRole = models.UserRoleStudent
	}

	if newRole == user.Role {
		return string(newRole), nil
	}

	_, err = s.usersCollection.UpdateOne(ctx,
		bson.M{"_id": userOID, "school_id": schoolOID},
		bson.M{"$set": bson.M{"role": newRole}},
	)
	if err != nil {
		return "", err
	}
	return string(newRole), nil
}

// hasCourses returns true if the user has at least one Google Classroom course.
// Uses pageSize=1 to minimise response size.
func (s *RoleService) hasCourses(ctx context.Context, client *http.Client) (bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		"https://classroom.googleapis.com/v1/courses?pageSize=1", nil)
	if err != nil {
		return false, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return false, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return false, fmt.Errorf("classroom api error: %s", string(body))
	}
	var result struct {
		Courses []struct{} `json:"courses"`
	}
	if err := json.Unmarshal(body, &result); err != nil {
		return false, err
	}
	return len(result.Courses) > 0, nil
}
