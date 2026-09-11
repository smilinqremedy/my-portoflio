package main

import (
	"bytes"
	"crypto/rand"
	"crypto/subtle"
	"database/sql"
	"encoding/base64"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"maison-royale/database"
)

type Room struct {
	ID          int
	Name        string
	Description string
	Price       int
	Capacity    int
}

type Booking struct {
	ID         int
	RoomName   string
	CheckIn    string
	CheckOut   string
	Guests     int
	GuestName  string
	GuestEmail string
	GuestPhone string
	Price      int
	Nights     int
	Total      int
}

type RoomAvailability struct {
	Name      string
	Available bool
}

type DashboardData struct {
	TotalBookings  int
	TotalRooms     int
	AvailableRooms int
	TotalRevenue   int
	Bookings       []Booking
	CheckIn        string
	CheckOut       string
	Availability   []RoomAvailability
	CSRFToken      string
}

var rooms = []Room{
	{
		ID:          1,
		Name:        "Deluxe King Room",
		Description: "A spacious room with a king-size bed and modern amenities.",
		Price:       85000,
		Capacity:    2,
	},
	{
		ID:          2,
		Name:        "Executive Suite",
		Description: "A premium suite designed for guests who want extra comfort.",
		Price:       140000,
		Capacity:    3,
	},
	{
		ID:          3,
		Name:        "Presidential Suite",
		Description: "Our most luxurious accommodation with an exceptional guest experience.",
		Price:       250000,
		Capacity:    4,
	},
}

// tmpl holds every page template. It is set once in main and read by the
// handlers and by renderError.
var tmpl *template.Template

var templateFuncs = template.FuncMap{
	"money": formatMoney,
}

const (
	sessionCookieName = "maison_admin_session"
	sessionDuration   = 8 * time.Hour

	maxLoginAttempts   = 5
	loginAttemptWindow = 15 * time.Minute
)

// session is the server-side half of an admin login. Keeping the state here
// rather than in the cookie means the cookie value is an unguessable handle
// instead of something a visitor can forge.
type session struct {
	csrfToken string
	expiresAt time.Time
}

var (
	sessionsMutex sync.Mutex
	sessions      = map[string]session{}
)

func randomToken() (string, error) {
	buffer := make([]byte, 32)

	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}

	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

// createSession returns the session token for the cookie and the CSRF token
// that admin forms must echo back.
func createSession() (string, string, error) {
	sessionToken, err := randomToken()

	if err != nil {
		return "", "", err
	}

	csrfToken, err := randomToken()

	if err != nil {
		return "", "", err
	}

	sessionsMutex.Lock()
	defer sessionsMutex.Unlock()

	sessions[sessionToken] = session{
		csrfToken: csrfToken,
		expiresAt: time.Now().Add(sessionDuration),
	}

	return sessionToken, csrfToken, nil
}

func lookupSession(r *http.Request) (session, bool) {
	cookie, err := r.Cookie(sessionCookieName)

	if err != nil {
		return session{}, false
	}

	sessionsMutex.Lock()
	defer sessionsMutex.Unlock()

	stored, ok := sessions[cookie.Value]

	if !ok {
		return session{}, false
	}

	if time.Now().After(stored.expiresAt) {
		delete(sessions, cookie.Value)
		return session{}, false
	}

	return stored, true
}

func destroySession(r *http.Request) {
	cookie, err := r.Cookie(sessionCookieName)

	if err != nil {
		return
	}

	sessionsMutex.Lock()
	defer sessionsMutex.Unlock()

	delete(sessions, cookie.Value)
}

func isAuthenticated(r *http.Request) bool {
	_, ok := lookupSession(r)

	return ok
}

func csrfToken(r *http.Request) string {
	stored, ok := lookupSession(r)

	if !ok {
		return ""
	}

	return stored.csrfToken
}

// hasValidCSRFToken guards the admin POST endpoints so another site cannot
// submit them on a logged-in admin's behalf.
func hasValidCSRFToken(r *http.Request) bool {
	stored, ok := lookupSession(r)

	if !ok {
		return false
	}

	submitted := r.FormValue("csrf_token")

	return subtle.ConstantTimeCompare(
		[]byte(stored.csrfToken),
		[]byte(submitted),
	) == 1
}

// loginAttempts tracks consecutive failures so a single client cannot grind
// through passwords unchecked.
type loginAttempts struct {
	count       int
	lastAttempt time.Time
}

var (
	loginAttemptsMutex sync.Mutex
	failedLogins       = map[string]loginAttempts{}
)

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)

	if err != nil {
		return r.RemoteAddr
	}

	return host
}

func loginBlocked(ip string) bool {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	attempts, ok := failedLogins[ip]

	if !ok {
		return false
	}

	if time.Since(attempts.lastAttempt) > loginAttemptWindow {
		delete(failedLogins, ip)
		return false
	}

	return attempts.count >= maxLoginAttempts
}

func recordFailedLogin(ip string) {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	attempts := failedLogins[ip]

	if time.Since(attempts.lastAttempt) > loginAttemptWindow {
		attempts.count = 0
	}

	attempts.count++
	attempts.lastAttempt = time.Now()

	failedLogins[ip] = attempts
}

func clearFailedLogins(ip string) {
	loginAttemptsMutex.Lock()
	defer loginAttemptsMutex.Unlock()

	delete(failedLogins, ip)
}

// formatMoney inserts thousands separators, so 85000 renders as "85,000".
// Exposed to the templates as "money" so server-rendered prices match the
// figures the booking form's calculator shows.
func formatMoney(amount int) string {
	digits := strconv.Itoa(amount)

	negative := strings.HasPrefix(digits, "-")

	if negative {
		digits = digits[1:]
	}

	var builder strings.Builder

	for index, digit := range digits {
		if index > 0 && (len(digits)-index)%3 == 0 {
			builder.WriteByte(',')
		}

		builder.WriteRune(digit)
	}

	if negative {
		return "-" + builder.String()
	}

	return builder.String()
}

// nightsBetween counts the nights between two YYYY-MM-DD dates, returning 0
// for anything it cannot make sense of.
func nightsBetween(checkIn string, checkOut string) int {
	checkInDate, err := time.Parse("2006-01-02", checkIn)

	if err != nil {
		return 0
	}

	checkOutDate, err := time.Parse("2006-01-02", checkOut)

	if err != nil {
		return 0
	}

	nights := int(checkOutDate.Sub(checkInDate) / (24 * time.Hour))

	if nights < 0 {
		return 0
	}

	return nights
}

// today returns midnight of the current local day, expressed in UTC so it can
// be compared with dates parsed by time.Parse.
func today() time.Time {
	now := time.Now()

	return time.Date(
		now.Year(),
		now.Month(),
		now.Day(),
		0, 0, 0, 0,
		time.UTC,
	)
}

// guestOptions lists the guest counts the booking form offers, capped at the
// largest room capacity so the dropdown cannot suggest an impossible party.
func guestOptions() []int {
	largest := 0

	for _, room := range rooms {
		if room.Capacity > largest {
			largest = room.Capacity
		}
	}

	options := []int{}

	for count := 1; count <= largest; count++ {
		options = append(options, count)
	}

	return options
}

// renderError shows a validation or not-found message on a styled page rather
// than as bare text from http.Error.
func renderError(w http.ResponseWriter, status int, title string, message string) {
	data := struct {
		Status  int
		Title   string
		Message string
	}{
		Status:  status,
		Title:   title,
		Message: message,
	}

	var page bytes.Buffer

	if err := tmpl.ExecuteTemplate(&page, "error.html", data); err != nil {
		log.Println("Error template error:", err)

		http.Error(w, message, status)

		return
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(status)

	if _, err := page.WriteTo(w); err != nil {
		log.Println("Failed to write error page:", err)
	}
}

func requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !isAuthenticated(r) {
			http.Redirect(
				w,
				r,
				"/admin/login",
				http.StatusSeeOther,
			)
			return
		}

		next(w, r)
	}
}

func loadDashboardData(db *sql.DB) (DashboardData, error) {
	var totalBookings int

	err := db.QueryRow(
		"SELECT COUNT(*) FROM bookings",
	).Scan(&totalBookings)

	if err != nil {
		return DashboardData{}, err
	}

	bookings := []Booking{}
	totalRevenue := 0

	rows, err := db.Query(`
		SELECT
			bookings.id,
			bookings.check_in,
			bookings.check_out,
			bookings.guests,
			bookings.room_id,
			bookings.guest_name,
			bookings.guest_email,
			bookings.guest_phone
		FROM bookings
		ORDER BY bookings.id DESC
	`)

	if err != nil {
		return DashboardData{}, err
	}

	defer rows.Close()

	for rows.Next() {
		var booking Booking
		var roomID int

		err := rows.Scan(
			&booking.ID,
			&booking.CheckIn,
			&booking.CheckOut,
			&booking.Guests,
			&roomID,
			&booking.GuestName,
			&booking.GuestEmail,
			&booking.GuestPhone,
		)

		if err != nil {
			return DashboardData{}, err
		}

		for _, room := range rooms {
			if room.ID == roomID {
				booking.RoomName = room.Name
				booking.Price = room.Price
				break
			}
		}

		booking.Nights = nightsBetween(
			booking.CheckIn,
			booking.CheckOut,
		)

		booking.Total = booking.Price * booking.Nights

		totalRevenue += booking.Total

		bookings = append(bookings, booking)
	}

	if err := rows.Err(); err != nil {
		return DashboardData{}, err
	}

	availableRooms := len(rooms)

	for _, room := range rooms {
		var roomBookings int

		// A room only counts as unavailable if a stay is in progress today.
		// Counting every future booking made rooms look permanently occupied.
		err := db.QueryRow(`
			SELECT COUNT(*)
			FROM bookings
			WHERE room_id = ?
			AND check_in <= date('now')
			AND check_out > date('now')
		`, room.ID).Scan(&roomBookings)

		if err != nil {
			return DashboardData{}, err
		}

		if roomBookings > 0 {
			availableRooms--
		}
	}

	return DashboardData{
		TotalBookings:  totalBookings,
		TotalRooms:     len(rooms),
		AvailableRooms: availableRooms,
		TotalRevenue:   totalRevenue,
		Bookings:       bookings,
	}, nil
}

func main() {
	db := database.Connect()
	defer db.Close()

	tmpl = template.Must(
		template.New("maison-royale").
			Funcs(templateFuncs).
			ParseFiles(
				"templates/index.html",
				"templates/confirmation.html",
				"templates/unavailable.html",
				"templates/error.html",
				"templates/admin/dashboard.html",
				"templates/admin/login.html",
			),
	)

	renderLogin := func(w http.ResponseWriter, message string) {
		data := struct {
			Error string
		}{
			Error: message,
		}

		if err := tmpl.ExecuteTemplate(
			w,
			"login.html",
			data,
		); err != nil {
			log.Println("Login template error:", err)
		}
	}

	// =========================================================
	// ADMIN LOGIN
	// =========================================================

	http.HandleFunc("/admin/login", func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodGet {
			if isAuthenticated(r) {
				http.Redirect(
					w,
					r,
					"/admin",
					http.StatusSeeOther,
				)
				return
			}

			renderLogin(w, "")

			return
		}

		if r.Method != http.MethodPost {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(
				w,
				"Unable to process login",
				http.StatusBadRequest,
			)
			return
		}

		ip := clientIP(r)

		if loginBlocked(ip) {
			log.Println("Login temporarily blocked for", ip)

			renderLogin(
				w,
				"Too many failed attempts. Please try again later.",
			)

			return
		}

		username := r.FormValue("username")
		password := r.FormValue("password")

		adminUsername := os.Getenv("ADMIN_USERNAME")
		adminPasswordHash := os.Getenv("ADMIN_PASSWORD_HASH")

		if adminUsername == "" || adminPasswordHash == "" {
			log.Println("Admin credentials are not configured")

			http.Error(
				w,
				"Admin authentication is not configured",
				http.StatusInternalServerError,
			)

			return
		}

		usernameMatches := subtle.ConstantTimeCompare(
			[]byte(username),
			[]byte(adminUsername),
		) == 1

		// The hash is always compared, even for an unknown username, so the
		// response time does not reveal which field was wrong.
		passwordErr := bcrypt.CompareHashAndPassword(
			[]byte(adminPasswordHash),
			[]byte(password),
		)

		if !usernameMatches || passwordErr != nil {
			recordFailedLogin(ip)

			log.Println("Failed admin login from", ip)

			renderLogin(w, "Invalid username or password.")

			return
		}

		sessionToken, _, err := createSession()

		if err != nil {
			log.Println("Failed to create session:", err)

			http.Error(
				w,
				"Unable to start session",
				http.StatusInternalServerError,
			)

			return
		}

		clearFailedLogins(ip)

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    sessionToken,
			Path:     "/admin",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   r.TLS != nil,
			MaxAge:   int(sessionDuration.Seconds()),
		})

		log.Println("Admin login successful")

		http.Redirect(
			w,
			r,
			"/admin",
			http.StatusSeeOther,
		)
	})

	// =========================================================
	// ADMIN LOGOUT
	// =========================================================

	http.HandleFunc("/admin/logout", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(
				w,
				"Unable to process logout",
				http.StatusBadRequest,
			)
			return
		}

		if !hasValidCSRFToken(r) {
			http.Error(
				w,
				"Invalid or expired form token",
				http.StatusForbidden,
			)
			return
		}

		destroySession(r)

		http.SetCookie(w, &http.Cookie{
			Name:     sessionCookieName,
			Value:    "",
			Path:     "/admin",
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   r.TLS != nil,
			MaxAge:   -1,
		})

		log.Println("Admin logged out")

		http.Redirect(
			w,
			r,
			"/admin/login",
			http.StatusSeeOther,
		)
	})

	// =========================================================
	// ADMIN CANCEL BOOKING
	// =========================================================

	http.HandleFunc("/admin/bookings/cancel", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(
				w,
				"Unable to process cancellation",
				http.StatusBadRequest,
			)
			return
		}

		if !hasValidCSRFToken(r) {
			http.Error(
				w,
				"Invalid or expired form token",
				http.StatusForbidden,
			)
			return
		}

		bookingID, err := strconv.Atoi(
			r.FormValue("booking_id"),
		)

		if err != nil || bookingID <= 0 {
			http.Error(
				w,
				"Invalid booking ID",
				http.StatusBadRequest,
			)
			return
		}

		result, err := db.Exec(
			"DELETE FROM bookings WHERE id = ?",
			bookingID,
		)

		if err != nil {
			log.Println(
				"Booking cancellation error:",
				err,
			)

			http.Error(
				w,
				"Unable to cancel booking",
				http.StatusInternalServerError,
			)

			return
		}

		rowsAffected, err := result.RowsAffected()

		if err != nil {
			log.Println(
				"Unable to verify booking cancellation:",
				err,
			)

			http.Error(
				w,
				"Unable to verify cancellation",
				http.StatusInternalServerError,
			)

			return
		}

		if rowsAffected == 0 {
			http.Error(
				w,
				"Booking not found",
				http.StatusNotFound,
			)

			return
		}

		log.Printf(
			"Booking #%d cancelled successfully",
			bookingID,
		)

		http.Redirect(
			w,
			r,
			"/admin",
			http.StatusSeeOther,
		)
	}))

	// =========================================================
	// ADMIN DASHBOARD
	// =========================================================

	http.HandleFunc("/admin", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		data, err := loadDashboardData(db)

		if err != nil {
			log.Println(
				"Failed to load dashboard:",
				err,
			)

			http.Error(
				w,
				"Unable to load dashboard",
				http.StatusInternalServerError,
			)

			return
		}

		data.CSRFToken = csrfToken(r)

		if err := tmpl.ExecuteTemplate(
			w,
			"dashboard.html",
			data,
		); err != nil {
			log.Println(
				"Dashboard template error:",
				err,
			)

			http.Error(
				w,
				"Unable to render dashboard",
				http.StatusInternalServerError,
			)
		}
	}))

	// =========================================================
	// ADMIN ROOM AVAILABILITY
	// =========================================================

	http.HandleFunc("/admin/availability", requireAdmin(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		checkIn := r.URL.Query().Get("check_in")
		checkOut := r.URL.Query().Get("check_out")

		if checkIn == "" || checkOut == "" {
			http.Error(
				w,
				"Check-in and check-out dates are required",
				http.StatusBadRequest,
			)
			return
		}

		checkInDate, err := time.Parse(
			"2006-01-02",
			checkIn,
		)

		if err != nil {
			http.Error(
				w,
				"Invalid check-in date",
				http.StatusBadRequest,
			)
			return
		}

		checkOutDate, err := time.Parse(
			"2006-01-02",
			checkOut,
		)

		if err != nil {
			http.Error(
				w,
				"Invalid check-out date",
				http.StatusBadRequest,
			)
			return
		}

		if !checkOutDate.After(checkInDate) {
			http.Error(
				w,
				"Check-out date must be after check-in date",
				http.StatusBadRequest,
			)
			return
		}

		availability := []RoomAvailability{}

		for _, room := range rooms {
			var existingBooking int

			err := db.QueryRow(`
				SELECT COUNT(*)
				FROM bookings
				WHERE room_id = ?
				AND check_in < ?
				AND check_out > ?
			`,
				room.ID,
				checkOut,
				checkIn,
			).Scan(&existingBooking)

			if err != nil {
				log.Println(
					"Failed to check room availability:",
					err,
				)

				http.Error(
					w,
					"Unable to check room availability",
					http.StatusInternalServerError,
				)

				return
			}

			availability = append(
				availability,
				RoomAvailability{
					Name:      room.Name,
					Available: existingBooking == 0,
				},
			)
		}

		availableCount := 0

		for _, room := range availability {
			if room.Available {
				availableCount++
			}
		}

		data, err := loadDashboardData(db)

		if err != nil {
			log.Println(
				"Failed to load dashboard data:",
				err,
			)

			http.Error(
				w,
				"Unable to load dashboard data",
				http.StatusInternalServerError,
			)

			return
		}

		data.AvailableRooms = availableCount
		data.CheckIn = checkIn
		data.CheckOut = checkOut
		data.Availability = availability
		data.CSRFToken = csrfToken(r)

		if err := tmpl.ExecuteTemplate(
			w,
			"dashboard.html",
			data,
		); err != nil {
			log.Println(
				"Availability template error:",
				err,
			)

			http.Error(
				w,
				"Unable to show availability",
				http.StatusInternalServerError,
			)
		}
	}))

	// =========================================================
	// BOOKING HANDLER
	// =========================================================

	http.HandleFunc("/book", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			http.Error(
				w,
				"Method not allowed",
				http.StatusMethodNotAllowed,
			)
			return
		}

		if err := r.ParseForm(); err != nil {
			http.Error(
				w,
				"Unable to process booking",
				http.StatusBadRequest,
			)
			return
		}

		checkIn := r.FormValue("check_in")
		checkOut := r.FormValue("check_out")
		guestsText := r.FormValue("guests")
		roomIDText := r.FormValue("room")

		guestName := strings.TrimSpace(r.FormValue("guest_name"))
		guestEmail := strings.TrimSpace(r.FormValue("guest_email"))
		guestPhone := strings.TrimSpace(r.FormValue("guest_phone"))

		if guestName == "" {
			renderError(
				w,
				http.StatusBadRequest,
				"Name Required",
				"Please tell us the name your reservation should be held under.",
			)
			return
		}

		parsedEmail, err := mail.ParseAddress(guestEmail)

		if err != nil || parsedEmail.Address != guestEmail {
			renderError(
				w,
				http.StatusBadRequest,
				"Email Address Needed",
				"Please enter a valid email address so we can send your confirmation.",
			)
			return
		}

		if checkIn == "" || checkOut == "" {
			renderError(
				w,
				http.StatusBadRequest,
				"Dates Required",
				"Please choose both a check-in and a check-out date.",
			)
			return
		}

		checkInDate, err := time.Parse(
			"2006-01-02",
			checkIn,
		)

		if err != nil {
			renderError(
				w,
				http.StatusBadRequest,
				"Invalid Check-in Date",
				"We could not read your check-in date. Please choose it again.",
			)
			return
		}

		checkOutDate, err := time.Parse(
			"2006-01-02",
			checkOut,
		)

		if err != nil {
			renderError(
				w,
				http.StatusBadRequest,
				"Invalid Check-out Date",
				"We could not read your check-out date. Please choose it again.",
			)
			return
		}

		if checkInDate.Before(today()) {
			renderError(
				w,
				http.StatusBadRequest,
				"Dates In The Past",
				"Your check-in date has already passed. Please choose a date from today onwards.",
			)
			return
		}

		if !checkOutDate.After(checkInDate) {
			renderError(
				w,
				http.StatusBadRequest,
				"Check-out Too Early",
				"Your check-out date must be at least one night after your check-in date.",
			)
			return
		}

		guests, err := strconv.Atoi(guestsText)

		if err != nil || guests < 1 {
			renderError(
				w,
				http.StatusBadRequest,
				"Guest Count Needed",
				"Please choose how many guests will be staying.",
			)
			return
		}

		roomID, err := strconv.Atoi(roomIDText)

		if err != nil {
			renderError(
				w,
				http.StatusBadRequest,
				"Room Not Recognised",
				"Please choose one of our rooms and try again.",
			)
			return
		}

		var selectedRoom Room
		foundRoom := false

		for _, room := range rooms {
			if room.ID == roomID {
				selectedRoom = room
				foundRoom = true
				break
			}
		}

		if !foundRoom {
			renderError(
				w,
				http.StatusBadRequest,
				"Room Not Found",
				"That room is no longer listed. Please choose another from our collection.",
			)
			return
		}

		if guests > selectedRoom.Capacity {
			renderError(
				w,
				http.StatusBadRequest,
				"Too Many Guests",
				fmt.Sprintf(
					"The %s sleeps up to %d guests. Please reduce your party or choose a larger suite.",
					selectedRoom.Name,
					selectedRoom.Capacity,
				),
			)
			return
		}

		var existingBooking int

		availabilityQuery := `
			SELECT COUNT(*)
			FROM bookings
			WHERE room_id = ?
			AND check_in < ?
			AND check_out > ?
		`

		err = db.QueryRow(
			availabilityQuery,
			roomID,
			checkOut,
			checkIn,
		).Scan(&existingBooking)

		if err != nil {
			log.Println(
				"Failed to check room availability:",
				err,
			)

			renderError(
				w,
				http.StatusInternalServerError,
				"Something Went Wrong",
				"We could not check availability just now. Please try again in a moment.",
			)

			return
		}

		if existingBooking > 0 {
			unavailableData := struct {
				RoomName string
				CheckIn  string
				CheckOut string
			}{
				RoomName: selectedRoom.Name,
				CheckIn:  checkIn,
				CheckOut: checkOut,
			}

			if err := tmpl.ExecuteTemplate(
				w,
				"unavailable.html",
				unavailableData,
			); err != nil {
				log.Println(
					"Unavailable template error:",
					err,
				)

				http.Error(
					w,
					"Unable to show availability message",
					http.StatusInternalServerError,
				)
			}

			return
		}

		query := `
			INSERT INTO bookings (
				check_in,
				check_out,
				guests,
				room_id,
				guest_name,
				guest_email,
				guest_phone
			)
			VALUES (?, ?, ?, ?, ?, ?, ?)
		`

		result, err := db.Exec(
			query,
			checkIn,
			checkOut,
			guests,
			roomID,
			guestName,
			guestEmail,
			guestPhone,
		)

		if err != nil {
			log.Println(
				"Failed to save booking:",
				err,
			)

			renderError(
				w,
				http.StatusInternalServerError,
				"Reservation Not Saved",
				"We could not save your reservation. Please try again in a moment.",
			)

			return
		}

		bookingID, err := result.LastInsertId()

		if err != nil {
			log.Println(
				"Failed to get booking ID:",
				err,
			)

			renderError(
				w,
				http.StatusInternalServerError,
				"Reservation Reference Unavailable",
				"Your reservation was saved but we could not read its reference. Please contact us to confirm.",
			)

			return
		}

		log.Println("Booking saved successfully")
		log.Println("Booking ID:", bookingID)
		log.Println("Check-in:", checkIn)
		log.Println("Check-out:", checkOut)
		log.Println("Guests:", guests)
		log.Println("Room:", selectedRoom.Name)
		log.Println("Guest:", guestName)

		nights := nightsBetween(checkIn, checkOut)

		total := selectedRoom.Price * nights

		confirmationData := struct {
			ID         int64
			RoomName   string
			CheckIn    string
			CheckOut   string
			Guests     int
			GuestName  string
			GuestEmail string
			GuestPhone string
			Price      int
			Nights     int
			Total      int
		}{
			ID:         bookingID,
			RoomName:   selectedRoom.Name,
			CheckIn:    checkIn,
			CheckOut:   checkOut,
			Guests:     guests,
			GuestName:  guestName,
			GuestEmail: guestEmail,
			GuestPhone: guestPhone,
			Price:      selectedRoom.Price,
			Nights:     nights,
			Total:      total,
		}

		if err := tmpl.ExecuteTemplate(
			w,
			"confirmation.html",
			confirmationData,
		); err != nil {
			log.Println(
				"Confirmation template error:",
				err,
			)

			http.Error(
				w,
				"Unable to show confirmation",
				http.StatusInternalServerError,
			)
		}
	})

	// =========================================================
	// STATIC FILES
	// =========================================================

	http.Handle(
		"/static/",
		http.StripPrefix(
			"/static/",
			http.FileServer(http.Dir("static")),
		),
	)

	// =========================================================
	// HOMEPAGE
	// =========================================================

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			renderError(
				w,
				http.StatusNotFound,
				"Page Not Found",
				"We could not find the page you were looking for.",
			)
			return
		}

		data := struct {
			Rooms        []Room
			GuestOptions []int
		}{
			Rooms:        rooms,
			GuestOptions: guestOptions(),
		}

		if err := tmpl.ExecuteTemplate(
			w,
			"index.html",
			data,
		); err != nil {
			log.Println(
				"Template error:",
				err,
			)

			http.Error(
				w,
				"Unable to render page",
				http.StatusInternalServerError,
			)
		}
	})

	port := os.Getenv("PORT")
	if port == "" {
		port = "8081"
	}

	log.Printf("Hotel platform running on port %s", port)

	if err := http.ListenAndServe(":"+port, nil); err != nil {
		log.Fatal(err)
	}

}
