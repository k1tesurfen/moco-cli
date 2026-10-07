package api

import "time"

// Session is the response of GET /session for a personal API token.
type Session struct {
	ID   int64  `json:"id"`
	UUID string `json:"uuid"`
}

// Ref is a minimal {id, name} reference used in nested resources.
type Ref struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// UserRef is a minimal user reference.
type UserRef struct {
	ID        int64  `json:"id"`
	Firstname string `json:"firstname"`
	Lastname  string `json:"lastname"`
}

// Task is a task ("Leistung") of an assigned project.
type Task struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Active   bool   `json:"active"`
	Billable bool   `json:"billable"`
}

// Project is an entry of GET /projects/assigned.
type Project struct {
	ID         int64  `json:"id"`
	Identifier string `json:"identifier"`
	Name       string `json:"name"`
	Active     bool   `json:"active"`
	Billable   bool   `json:"billable"`
	Customer   Ref    `json:"customer"`
	Tasks      []Task `json:"tasks"`
	Contract   *struct {
		UserID int64 `json:"user_id"`
		Active bool  `json:"active"`
	} `json:"contract"`
}

// Presence is a working-time block ("Arbeitszeit") of one day. From/To are "HH:MM";
// To is empty while the presence is open.
type Presence struct {
	ID           int64     `json:"id"`
	Date         string    `json:"date"`
	From         string    `json:"from"`
	To           string    `json:"to"`
	IsHomeOffice bool      `json:"is_home_office"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// PresenceInput is the body for creating or updating a presence. Empty fields are omitted.
type PresenceInput struct {
	Date         string `json:"date,omitempty"`
	From         string `json:"from,omitempty"`
	To           string `json:"to,omitempty"`
	IsHomeOffice *bool  `json:"is_home_office,omitempty"`
}

// Activity is a time entry. Use Seconds; Hours is a rounded convenience value.
type Activity struct {
	ID             int64      `json:"id"`
	Date           string     `json:"date"`
	Seconds        int        `json:"seconds"`
	WorkedSeconds  int        `json:"worked_seconds"`
	Description    string     `json:"description"`
	Billed         bool       `json:"billed"`
	Billable       bool       `json:"billable"`
	Project        Ref        `json:"project"`
	Task           Ref        `json:"task"`
	Customer       Ref        `json:"customer"`
	User           UserRef    `json:"user"`
	TimerStartedAt *time.Time `json:"timer_started_at"`
	CreatedAt      time.Time  `json:"created_at"`
	UpdatedAt      time.Time  `json:"updated_at"`
}

// TimerRunning reports whether this activity has a running timer.
func (a Activity) TimerRunning() bool { return a.TimerStartedAt != nil }

// ActivityInput is the body for creating or updating an activity. Billable and tag are
// intentionally not exposed; MOCO/the project decides.
type ActivityInput struct {
	Date        string `json:"date,omitempty"`
	ProjectID   int64  `json:"project_id,omitempty"`
	TaskID      int64  `json:"task_id,omitempty"`
	Seconds     *int   `json:"seconds,omitempty"`
	Description string `json:"description,omitempty"`
}
