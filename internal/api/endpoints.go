package api

import (
	"context"
	"net/http"
	"net/url"
	"strconv"
)

// Session returns the identity behind the API token.
func (c *Client) Session(ctx context.Context) (Session, error) {
	var s Session
	_, err := c.do(ctx, http.MethodGet, c.url("/session", nil), nil, &s)
	return s, err
}

// AssignedProjects returns the active projects the user is assigned to, including their tasks.
func (c *Client) AssignedProjects(ctx context.Context) ([]Project, error) {
	return getAll[Project](ctx, c, "/projects/assigned", url.Values{"active": {"true"}})
}

// Presences returns the user's presences between from and to (inclusive, YYYY-MM-DD).
func (c *Client) Presences(ctx context.Context, userID int64, from, to string) ([]Presence, error) {
	return getAll[Presence](ctx, c, "/users/presences", rangeQuery(userID, from, to))
}

// CreatePresence creates a presence.
func (c *Client) CreatePresence(ctx context.Context, in PresenceInput) (Presence, error) {
	var p Presence
	_, err := c.do(ctx, http.MethodPost, c.url("/users/presences", nil), in, &p)
	return p, err
}

// UpdatePresence changes the given fields of a presence.
func (c *Client) UpdatePresence(ctx context.Context, id int64, in PresenceInput) (Presence, error) {
	var p Presence
	_, err := c.do(ctx, http.MethodPatch, c.url("/users/presences/"+itoa(id), nil), in, &p)
	return p, err
}

// DeletePresence deletes a presence.
func (c *Client) DeletePresence(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, c.url("/users/presences/"+itoa(id), nil), nil, nil)
	return err
}

// Activities returns the user's activities between from and to (inclusive, YYYY-MM-DD).
// user_id is mandatory in practice: without it MOCO returns the activities of all colleagues.
func (c *Client) Activities(ctx context.Context, userID int64, from, to string) ([]Activity, error) {
	return getAll[Activity](ctx, c, "/activities", rangeQuery(userID, from, to))
}

// CreateActivity creates an activity.
func (c *Client) CreateActivity(ctx context.Context, in ActivityInput) (Activity, error) {
	var a Activity
	_, err := c.do(ctx, http.MethodPost, c.url("/activities", nil), in, &a)
	return a, err
}

// UpdateActivity changes the given fields of an activity.
func (c *Client) UpdateActivity(ctx context.Context, id int64, in ActivityInput) (Activity, error) {
	var a Activity
	_, err := c.do(ctx, http.MethodPatch, c.url("/activities/"+itoa(id), nil), in, &a)
	return a, err
}

// DeleteActivity deletes an activity.
func (c *Client) DeleteActivity(ctx context.Context, id int64) error {
	_, err := c.do(ctx, http.MethodDelete, c.url("/activities/"+itoa(id), nil), nil, nil)
	return err
}

// StartTimer starts the timer on one of today's activities.
func (c *Client) StartTimer(ctx context.Context, id int64) (Activity, error) {
	var a Activity
	_, err := c.do(ctx, http.MethodPatch, c.url("/activities/"+itoa(id)+"/start_timer", nil), nil, &a)
	return a, err
}

// StopTimer stops the timer of an activity.
func (c *Client) StopTimer(ctx context.Context, id int64) (Activity, error) {
	var a Activity
	_, err := c.do(ctx, http.MethodPatch, c.url("/activities/"+itoa(id)+"/stop_timer", nil), nil, &a)
	return a, err
}

func rangeQuery(userID int64, from, to string) url.Values {
	return url.Values{"user_id": {itoa(userID)}, "from": {from}, "to": {to}}
}

func itoa(id int64) string { return strconv.FormatInt(id, 10) }
