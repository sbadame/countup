package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// setupTestDB creates a temporary test database
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()

	// Create temporary file for SQLite
	tmpFile, err := os.CreateTemp("", "test-timers-*.db")
	if err != nil {
		t.Fatalf("Failed to create temp file: %v", err)
	}
	tmpFile.Close()

	// Open database connection
	db, err := sql.Open("sqlite", tmpFile.Name())
	if err != nil {
		t.Fatalf("Failed to open database: %v", err)
	}

	// Create schema
	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS timer (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL,
		lasttime TEXT NOT NULL,
		frequency INTEGER NOT NULL
	);`)
	if err != nil {
		t.Fatalf("Failed to create schema: %v", err)
	}

	// Setup teardown to close and remove the database
	t.Cleanup(func() {
		db.Close()
		os.Remove(tmpFile.Name())
	})

	return db
}

// insertTestData adds test timers to the database
func insertTestData(t *testing.T, db *sql.DB) []CountDown {
	t.Helper()

	// Sample time values
	now := time.Now()
	yesterday := now.Add(-24 * time.Hour)

	// Test data to insert
	testTimers := []CountDown{
		{
			Name:        "Test Timer 1",
			Description: "First test timer",
			LastTime:    yesterday,
			Frequency:   24 * time.Hour,
		},
		{
			Name:        "Test Timer 2",
			Description: "Second test timer",
			LastTime:    time.Time{}, // Zero time
			Frequency:   7 * 24 * time.Hour,
		},
	}

	// Insert each timer
	for i, timer := range testTimers {
		result, err := db.Exec(
			`INSERT INTO timer (name, description, lasttime, frequency) VALUES (?, ?, ?, ?)`,
			timer.Name, timer.Description,
			timer.LastTime.Format(time.RFC3339),
			timer.Frequency,
		)
		if err != nil {
			t.Fatalf("Failed to insert test data: %v", err)
		}

		id, err := result.LastInsertId()
		if err != nil {
			t.Fatalf("Failed to get last insert ID: %v", err)
		}

		testTimers[i].Id = id
	}

	return testTimers
}

// TestHTTPError tests the HTTP error wrapper
func TestHTTPError(t *testing.T) {
	tests := []struct {
		name           string
		err            error
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "nil error",
			err:            nil,
			expectedStatus: http.StatusOK,
			expectedBody:   "",
		},
		{
			name:           "standard error",
			err:            io.EOF,
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "EOF\n",
		},
		{
			name:           "http error",
			err:            httpError{code: http.StatusNotFound, err: io.EOF},
			expectedStatus: http.StatusNotFound,
			expectedBody:   "EOF\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create a test handler that returns the specified error
			handler := ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
				return tt.err
			})

			// Create a test request
			req := httptest.NewRequest("GET", "/", nil)
			w := httptest.NewRecorder()

			// Call the handler
			handler(w, req)

			// Check status code
			if w.Code != tt.expectedStatus {
				t.Errorf("Expected status %d, got %d", tt.expectedStatus, w.Code)
			}

			// Check response body
			if w.Body.String() != tt.expectedBody {
				t.Errorf("Expected body %q, got %q", tt.expectedBody, w.Body.String())
			}
		})
	}
}

// TestCountDownNextDue tests the NextDue method of CountDown
func TestCountDownNextDue(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name      string
		countdown CountDown
		expectFn  func(time.Time) bool
	}{
		{
			name: "with last time set",
			countdown: CountDown{
				LastTime:  now.Add(-24 * time.Hour),
				Frequency: 48 * time.Hour,
			},
			expectFn: func(result time.Time) bool {
				expected := now.Add(24 * time.Hour)
				diff := result.Sub(expected)
				return diff > -time.Second && diff < time.Second
			},
		},
		{
			name: "with zero last time",
			countdown: CountDown{
				LastTime:  time.Time{}, // Zero time
				Frequency: 24 * time.Hour,
			},
			expectFn: func(result time.Time) bool {
				expected := now.Add(24 * time.Hour)
				diff := result.Sub(expected)
				return diff > -time.Second && diff < time.Second
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tt.countdown.NextDue()
			if !tt.expectFn(result) {
				t.Errorf("NextDue() returned unexpected time: %v", result)
			}
		})
	}
}

// TestHomePageHandler tests the home page handler
func TestHomePageHandler(t *testing.T) {
	db := setupTestDB(t)
	testTimers := insertTestData(t, db)

	// Set up a request
	req := httptest.NewRequest("GET", "/", nil)
	w := httptest.NewRecorder()

	// Execute the handler
	(&Server{db}).mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %v", w.Code)
	}

	// Check that all test timer names appear in the response
	for _, timer := range testTimers {
		if !strings.Contains(w.Body.String(), timer.Name) {
			t.Errorf("Response does not contain timer name: %s", timer.Name)
		}
	}
}

// TestGetTimerHandler tests the GET /timer/{id} handler
func TestGetTimerHandler(t *testing.T) {
	db := setupTestDB(t)
	testTimers := insertTestData(t, db)

	// Test getting a timer that exists
	t.Run("existing timer", func(t *testing.T) {
		// Set up a request
		req := httptest.NewRequest("GET", fmt.Sprintf("/timer/%d", testTimers[0].Id), nil)
		req = req.WithContext(context.WithValue(req.Context(), struct{}{}, "id"))
		w := httptest.NewRecorder()

		// Execute the handler
		(&Server{db}).mux().ServeHTTP(w, req)

		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}

		// Check that timer name appears in the response
		if !strings.Contains(w.Body.String(), testTimers[0].Name) {
			t.Errorf("Response does not contain timer name: %s", testTimers[0].Name)
		}
	})

	// Test getting a timer that doesn't exist
	t.Run("non-existent timer", func(t *testing.T) {
		// Set up a request
		req := httptest.NewRequest("GET", "/timer/999", nil)
		req = req.WithContext(context.WithValue(req.Context(), struct{}{}, "id"))
		w := httptest.NewRecorder()

		// Execute the handler
		(&Server{db}).mux().ServeHTTP(w, req)

		if w.Code != http.StatusNotFound {
			t.Errorf("Expected NotFound error, got %v", w.Code)
		}
	})
}

// TestCreateTimerHandler tests the POST /timer handler
func TestCreateTimerHandler(t *testing.T) {
	db := setupTestDB(t)

	// Helper function to count timers in the database
	countTimers := func() int {
		var count int
		row := db.QueryRow("SELECT COUNT(*) FROM timer")
		if err := row.Scan(&count); err != nil {
			t.Fatalf("Failed to count timers: %v", err)
		}
		return count
	}

	initialCount := countTimers()

	// Test creating a valid timer
	t.Run("valid timer", func(t *testing.T) {
		formData := url.Values{
			"name":           {"Test New Timer"},
			"description":    {"Created in test"},
			"lasttime":       {time.Now().Format("2006-01-02T15:04")},
			"frequencyValue": {"2"},
			"frequencyUnit":  {"86400000000000"}, // 2 days
		}

		// Set up a request
		req := httptest.NewRequest("POST", "/timer", strings.NewReader(formData.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.PostForm = formData
		w := httptest.NewRecorder()

		// Execute the handler
		(&Server{db}).mux().ServeHTTP(w, req)

		// Verify response
		if w.Code != http.StatusOK {
			t.Errorf("Expected status OK, got %v", w.Code)
		}

		// Check that timer was created
		if countTimers() != initialCount+1 {
			t.Errorf("Timer was not created in database")
		}

		// Check that timer name appears in the response
		if !strings.Contains(w.Body.String(), "Test New Timer") {
			t.Errorf("Response does not contain timer name")
		}
	})

	// Test creating an invalid timer (missing required fields)
	t.Run("invalid timer", func(t *testing.T) {
		formData := url.Values{
			"name":        {""},
			"description": {"Missing name should fail"},
			"lasttime":    {"invalid-time-format"},
		}

		// Set up a request
		req := httptest.NewRequest("POST", "/timer", strings.NewReader(formData.Encode()))
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		req.PostForm = formData
		w := httptest.NewRecorder()

		// Execute the handler
		(&Server{db}).mux().ServeHTTP(w, req)

		if w.Result().StatusCode != http.StatusBadRequest {
			t.Errorf("Expected BadRequest error, got %d", w.Result().StatusCode)
		}

		// Verify no new timer was created
		if countTimers() != initialCount+1 {
			t.Errorf("Invalid timer creation affected database")
		}
	})
}

// TestResetTimerHandler tests the POST /timer/{id}/reset handler
func TestResetTimerHandler(t *testing.T) {
	db := setupTestDB(t)
	testTimers := insertTestData(t, db)

	// Function to get last time for a timer
	getLastTime := func(id int64) time.Time {
		var lastTimeStr string
		row := db.QueryRow("SELECT lasttime FROM timer WHERE id = ?", id)
		if err := row.Scan(&lastTimeStr); err != nil {
			t.Fatalf("Failed to get lasttime: %v", err)
		}
		lastTime, err := time.Parse(time.RFC3339, lastTimeStr)
		if err != nil {
			t.Fatalf("Failed to parse lasttime: %v", err)
		}
		return lastTime
	}

	// Get initial last time
	initialLastTime := getLastTime(testTimers[0].Id)

	// Short delay to ensure time difference
	time.Sleep(10 * time.Millisecond)

	// Set up a request
	req := httptest.NewRequest("POST", fmt.Sprintf("/timer/%d/reset", testTimers[0].Id), nil)
	w := httptest.NewRecorder()

	// Execute the handler
	(&Server{db}).mux().ServeHTTP(w, req)

	// Verify response
	resp := w.Result()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("Expected status OK, got %v", resp.Status)
	}

	// Verify HX-Trigger header
	triggerHeader := resp.Header.Get("HX-Trigger")
	expectedTrigger := fmt.Sprintf("timerUpdate/%d", testTimers[0].Id)
	if triggerHeader != expectedTrigger {
		t.Errorf("Expected HX-Trigger %q, got %q", expectedTrigger, triggerHeader)
	}

	// Verify that the lasttime was updated
	updatedLastTime := getLastTime(testTimers[0].Id)
	if !updatedLastTime.After(initialLastTime) {
		t.Errorf("Last time was not updated. Initial: %v, Updated: %v", initialLastTime, updatedLastTime)
	}
}

// TestDeleteTimerHandler tests the DELETE /timer/{id} handler
func TestDeleteTimerHandler(t *testing.T) {
	db := setupTestDB(t)
	testTimers := insertTestData(t, db)

	// Helper function to check if timer exists
	timerExists := func(id int64) bool {
		var count int
		row := db.QueryRow("SELECT COUNT(*) FROM timer WHERE id = ?", id)
		if err := row.Scan(&count); err != nil {
			t.Fatalf("Failed to check timer existence: %v", err)
		}
		return count > 0
	}

	// Verify timer exists initially
	if !timerExists(testTimers[0].Id) {
		t.Fatalf("Test timer does not exist before deletion test")
	}

	// Set up a request
	req := httptest.NewRequest("DELETE", fmt.Sprintf("/timer/%d", testTimers[0].Id), nil)
	w := httptest.NewRecorder()

	// Execute the handler
	(&Server{db}).mux().ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("Expected status OK, got %v", w.Code)
	}

	// Verify timer was deleted
	if timerExists(testTimers[0].Id) {
		t.Errorf("Timer still exists after deletion")
	}
}

// TestHTTPErrorInterface tests the HTTPError interface implementation
func TestHTTPErrorInterface(t *testing.T) {
	err := httpError{
		code: http.StatusNotFound,
		err:  fmt.Errorf("resource not found"),
	}

	// Test HTTPStatusCode method
	if err.HTTPStatusCode() != http.StatusNotFound {
		t.Errorf("Expected status code %d, got %d", http.StatusNotFound, err.HTTPStatusCode())
	}

	// Test Error method
	expectedMsg := "resource not found"
	if err.Error() != expectedMsg {
		t.Errorf("Expected error message %q, got %q", expectedMsg, err.Error())
	}

	// Test interface implementation
	var httpErr HTTPError = err
	if httpErr.HTTPStatusCode() != http.StatusNotFound {
		t.Errorf("Interface implementation failed")
	}
}
