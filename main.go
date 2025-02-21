package main

import (
	"bytes"
	"flag"
	"fmt"
	"html/template"
	"io"
	"log"
	"net/http"
	"strconv"
	"time"

	"database/sql"
	_ "modernc.org/sqlite"
)

// HTTPError makes it easy to create errors that map to HTTP Status Codes.
type HTTPError interface {
	HTTPStatusCode() int
}

type httpError struct {
	code int
	err  error
}

func (h httpError) HTTPStatusCode() int {
	return h.code
}

func (h httpError) Error() string {
	return h.err.Error()
}

func ErrorHTTPHandler(h func(http.ResponseWriter, *http.Request) error) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		err := h(w, r)
		if err == nil {
			return
		}

		sc := http.StatusInternalServerError
		if httpErr, ok := err.(HTTPError); ok {
			sc = httpErr.HTTPStatusCode()
		}
		if sc >= 500 {
			log.Printf("%d Response for Request: %s %s, %s\n", sc, r.Method, r.URL, err.Error())
		}
		http.Error(w, err.Error(), sc)
	}
}

type CountDown struct {
	Id                int64 // Primary Key for the different CountDowns
	Name, Description string
	LastTime          time.Time
	Frequency         time.Duration
}

func (c CountDown) NextDue() time.Time {
	if c.LastTime.IsZero() {
		return time.Now().Add(c.Frequency)
	}
	return c.LastTime.Add(c.Frequency)
}

var (
	timer = template.Must(template.New("timer").Parse(`
<div id="timer-{{.Id}}" hx-get="timer/{{.Id}}" hx-trigger="timerUpdate/{{.Id}}" class="border-bottom d-flex pt-3 text-muted">
<div class="p-3">
  <strong class="text-dark">{{.Name}}</strong>
  <button type="button" class="btn btn-sm btn-success" hx-post="timer/{{.Id}}/reset" hx-swap="none"><i class="bi bi-check-circle"></i></button>
  <p class="my-0">
      {{.Description}}
      {{ if .Description }}<br>{{end}}
      {{ if not .LastTime.IsZero -}}
	Last happened <span data-locale-date-string="{{.LastTime}}"></span>
	(<span class="last-time" data-format-distance-to-now="{{/* RFC3339 */}}{{.LastTime.Format "2006-01-02T15:04:05Z07:00"}}"></span> ago)
	<br>
      {{- end}}
      {{ if .Frequency -}}
	Do it again in <span class="last-time" data-format-distance-to-now="{{/* RFC3339 */}}{{.NextDue.Format "2006-01-02T15:04:05Z07:00"}}"></span>
      {{- end}}
  </p>
  <button type="button" class="btn btn-sm btn-outline-danger" hx-delete="timer/{{.Id}}" hx-swap="delete" hx-target="#timer-{{.Id}}"><i class="bi bi-trash"></i></button>
</div>
</div>
`))

	homePage = template.Must(timer.New("homepage").Parse(`
<!DOCTYPE html>
<html>
  <head>
    <title>Countdown</title>
    <meta name="viewport" content="width=device-width, initial-scale=1">
    <script src="https://unpkg.com/htmx.org@2.0.4" integrity="sha384-HGfztofotfshcF7+8n44JQL2oJmowVChPTg48S+jvZoztPfvwD79OC/LTtG6dMp+" crossorigin="anonymous"></script>
    <link href="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/css/bootstrap.min.css" rel="stylesheet" integrity="sha384-T3c6CoIi6uLrA9TneNEoa7RxnatzjcDSCmG1MXxSR1GAsXEV/Dwwykc2MPK8M2HN" crossorigin="anonymous">
    <link rel="stylesheet" href="https://cdn.jsdelivr.net/npm/bootstrap-icons@1.11.3/font/bootstrap-icons.min.css">
    <style>
      .floating-button {
        position: fixed;
        bottom: 2rem;
        right: 2rem;
        z-index: 1030; /* https://getbootstrap.com/docs/5.0/layout/z-index/ */
        box-shadow: 0 4px 10px rgba(0, 0, 0, 0.15);
        border-radius: 50%;
        width: 60px;
        height: 60px;
        display: flex;
        align-items: center;
        justify-content: center;
      }
    </style>
  </head>
  <body class="bg-light">
    <header class="d-flex flex-wrap justify-content-center py-3 mb-4 border-bottom">
      <a href="/" class="d-flex align-items-center mb-3 mb-md-0 me-md-auto text-dark text-decoration-none">
        <span class="fs-4">Count up Timer</span>
      </a>
    </header>

    <main id="timerList" class="container">
      <div class="bg-body rounded shadow-sm">
	{{range .}}
	  {{template "timer" .}}
	{{end}}
      </div>
    </main>

    <!-- <button type="button" class="btn btn-primary" data-bs-toggle="modal" data-bs-target="#createTimer">New Timer</button> -->

    <!-- Floating action button -->
    <button type="button" class="btn btn-primary floating-button" data-bs-toggle="modal" data-bs-target="#createTimer">
      <i class="bi bi-plus fs-4"></i>
    </button>

    {{/* Form for creating timers */}}
    <div class="modal fade" id="createTimer" tabindex="-1" aria-labelledby="exampleModalLabel" aria-hidden="true">
      <form hx-post="/timer" hx-target="#timerList" hx-swap="afterbegin">
	<div class="modal-dialog">
	  <div class="modal-content">
	    <div class="modal-header">
	      <h5 class="modal-title" id="exampleModalLabel">Create Timer</h5>
	      <button type="button" class="btn-close" data-bs-dismiss="modal" aria-label="Close"></button>
	    </div>
	    <div class="modal-body">
		  <div class="mb-3">
		    <label for="timerName" class="form-label">Name</label>
		    <input type="text" class="form-control" name="name" id="timerName">
		  </div>
		  <div class="mb-3">
		    <label for="timerDescription" class="form-label">Description</label>
		    <textarea class="form-control" id="timerDescription" name="description"></textarea>
		  </div>
		  <div class="mb-3">
		    <label for="timerLastTime" class="form-label">Last time I did it</label>
		    <input type="datetime-local" id="timerLastTime" name="lasttime"></input>
		  </div>
		  <div class="mb-3">
                    <label for="timerFrequency" class="form-label">Do it every:</label>
                    <div class="input-group">
                      <input type="number" id="timerFrequencyValue" name="frequencyValue" class="form-control" min="1" value="1">
                      <select id="timerFrequencyUnit" name="frequencyUnit" class="form-select">
                        <option value="86400000000000">Days</option>
                        <option value="604800000000000">Weeks</option>
                        <option value="2592000000000000">Months</option>
                        <option value="31536000000000000">Years</option>
                      </select>
                    </div>
                  </div>
	    </div>
	    <div class="modal-footer">
	      <button type="button" class="btn btn-secondary" data-bs-dismiss="modal">Close</button>
	      <button type="submit" class="btn btn-primary" data-bs-dismiss="modal">Create</button>
	    </div>
	  </div>
	</div>
      </form>
    </div>

    {{/* Bring in some more javascript now that we've got the styles and DOM loaded. */}}
    <script src="https://cdn.jsdelivr.net/npm/date-fns@3.6.0/cdn.min.js"></script>
    <script src="https://cdn.jsdelivr.net/npm/bootstrap@5.3.2/dist/js/bootstrap.bundle.min.js" integrity="sha384-C6RzsynM9kWDrMNeT87bh95OGNyZPhcTNXj1NW7RuBCsyN/o0jlpcV8Qyq46cDfL" crossorigin="anonymous"></script>
    <script>
      {{/* Format the times to local locale with a plain english description of how long ago. */}}
      function renderTimer() {
	document.querySelectorAll('[data-format-distance-to-now]').forEach(e => e.innerText = dateFns.formatDistanceToNow(e.dataset.formatDistanceToNow));
	document.querySelectorAll('[data-locale-date-string]').forEach(e => e.innerText = new Date(e.dataset.localeDateString).toLocaleDateString());
      }
      renderTimer()
      document.addEventListener('htmx:afterSwap', renderTimer);
      document.querySelector("input[type='datetime-local']").value = dateFns.format(new Date(), "yyyy-MM-dd'T'HH:mm");
    </script>
  </body>
</html>
`))
)

func main() {

	var dbFile = flag.String("db-file", "timers.db", "The sqlite file to read and write state from.")
	var dbRecreate = flag.Bool("db-recreate", false, "Drops data in the file and creates the necessary schemas.")
	var dbPopulateTestData = flag.Bool("db-populate-test-data", false, "Inserts rows of test data into the table.")

	var httpPort = flag.Int("port", 8080, "The http port to expose the server on.")

	flag.Parse()

	// Initialiaze a DB connection.
	db, err := sql.Open("sqlite", *dbFile)
	if err != nil {
		log.Fatal(err)
	}
	defer db.Close()

	if *dbRecreate {
		if _, err = db.Exec(`DROP TABLE IF EXISTS timer;`); err != nil {
			log.Fatal(err)
		}
	}

	_, err = db.Exec(`
	CREATE TABLE IF NOT EXISTS timer (
		id INTEGER PRIMARY KEY,
		name TEXT NOT NULL,
		description TEXT NOT NULL,
		lasttime TEXT NOT NULL,
		frequency INTEGER NOT NULL
	);`)
	if err != nil {
		log.Fatal(err)
	}

	if *dbPopulateTestData {
		_, err = db.Exec(`
		INSERT INTO timer
			(name, description, lastTime, frequency)
		VALUES
			('Sandro Test',      '', '2025-01-02T00:00:00-05:00', 0),
			('Check Money',      '', '2025-01-02T00:00:00-05:00', 2592000000000000),
			('Go to gym',        '', '2025-01-02T00:00:00-05:00', 259200000000000),
			('Check on Mike',    '', '2025-01-02T00:00:00-05:00', 2 * 2592000000000000),
			('Start new coffee', '', '2025-01-02T00:00:00-05:00', 86400000000000),
			('Make Pizza',       '', '',                          2 * 2592000000000000)
		`)
		if err != nil {
			log.Fatal(err)
		}
	}

	http.HandleFunc("GET /", ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
		var timers []CountDown

		rows, err := db.QueryContext(r.Context(), `SELECT id, name, description, lastTime, frequency FROM timer`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var c CountDown
			var lt string
			if err := rows.Scan(&c.Id, &c.Name, &c.Description, &lt, &c.Frequency); err != nil {
				return err
			}

			if lt != "" {
				if lastTime, err := time.Parse(time.RFC3339, lt); err != nil {
					return err
				} else {
					c.LastTime = lastTime
				}
			}

			timers = append(timers, c)
		}

		// Write to a buffer first to avoid writing partial results if an error occurs during template execution
		var buf bytes.Buffer
		if err := homePage.Execute(&buf, timers); err != nil {
			return err
		}

		// OK, no errors, copy the written string out.
		io.Copy(w, &buf)
		return nil
	}))

	http.HandleFunc("GET /timer/{id}", ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing id : %w", err)}
		}

		var c CountDown
		var lt string

		row := db.QueryRowContext(r.Context(), `SELECT id, name, description, lastTime, frequency FROM timer WHERE id = ?`, id)
		if err := row.Scan(&c.Id, &c.Name, &c.Description, &lt, &c.Frequency); err != nil {
			if err == sql.ErrNoRows {
				return httpError{http.StatusNotFound, fmt.Errorf("No timer with id: %s", id)}
			}
			return err
		}
		if lt != "" {
			if lastTime, err := time.Parse(time.RFC3339, lt); err != nil {
				return err
			} else {
				c.LastTime = lastTime
			}
		}

		return timer.Execute(w, c)
	}))

	http.HandleFunc("DELETE /timer/{id}", ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing id : %w", err)}
		}

		result, err := db.ExecContext(r.Context(), `DELETE FROM timer WHERE id = ?`, id)
		if err != nil {
			return err
		}
		if rows, err := result.RowsAffected(); err != nil {
			return err
		} else if rows == 0 {
			return httpError{http.StatusNotFound, fmt.Errorf("No timer with id: %s", id)}
		} else if rows != 1 {
			return fmt.Errorf("Exepected only 1 row to be deleted but instead %d where.", rows)
		}
		return nil
	}))

	http.HandleFunc("POST /timer", ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
		if err := r.ParseForm(); err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing form : %w", err)}
		}

		lastTime, err := time.Parse("2006-01-02T15:04", r.Form.Get("lasttime"))
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing query 'lasttime': %w", err)}
		}

		// Parse frequency parameters
		frequencyValue, err := strconv.ParseInt(r.Form.Get("frequencyValue"), 10, 64)
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing frequency value: %w", err)}
		}

		frequencyUnit, err := strconv.ParseInt(r.Form.Get("frequencyUnit"), 10, 64)
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing frequency unit: %w", err)}
		}

		// Calculate total frequency in nanoseconds, to match with Duration.
		frequency := time.Duration(frequencyValue * frequencyUnit)

		cd := CountDown{
			Name:        r.Form.Get("name"),
			Description: r.Form.Get("description"),
			LastTime:    lastTime,
			Frequency:   frequency,
		}

		result, err := db.ExecContext(r.Context(),
			`INSERT INTO timer (name, description, lasttime, frequency) VALUES (?,?,?,?);`,
			cd.Name, cd.Description, lastTime.Format(time.RFC3339), cd.Frequency)
		if err != nil {
			return err
		}

		if cd.Id, err = result.LastInsertId(); err != nil {
			return err
		}

		return timer.Execute(w, cd)
	}))

	http.HandleFunc("POST /timer/{id}/reset", ErrorHTTPHandler(func(w http.ResponseWriter, r *http.Request) error {
		id, err := strconv.Atoi(r.PathValue("id"))
		if err != nil {
			return httpError{http.StatusBadRequest, fmt.Errorf("Error parsing id : %w", err)}
		}

		result, err := db.ExecContext(r.Context(), `UPDATE timer SET lasttime = ? WHERE id = ?`, time.Now().Format(time.RFC3339), id)
		if err != nil {
			return err
		}
		rows, err := result.RowsAffected()
		if err != nil {
			return err
		}
		if rows == 0 {
			return httpError{http.StatusNotFound, fmt.Errorf("No timer with id: %s", id)}
		}
		if rows > 1 {
			return fmt.Errorf("Expected only 1 row to be affect, but instead %d where", rows)
		}

		w.Header().Set("HX-Trigger", "timerUpdate/"+r.PathValue("id"))
		return nil
	}))

	log.Printf("Serving on :%d\n", *httpPort)
	log.Fatal(http.ListenAndServe(":"+strconv.Itoa(*httpPort), nil))
}
