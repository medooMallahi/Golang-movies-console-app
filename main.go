package main

import (
	"bufio"
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"os"
	"regexp"
	"strings"

	_ "github.com/lib/pq"
)

const (
	connStr     = "postgres://mohammedalmallahi:0592413118@localhost:5432/postgres?sslmode=disable"
	tmdbAPIKey  = "56fd9e9ff7af5f79cecbfecfd7643fce"
	tmdbBaseURL = "https://api.themoviedb.org/3"
)

type TMDbMovieResult struct {
	Page    int `json:"page"`
	Results []struct {
		ID          int    `json:"id"`
		Title       string `json:"title"`
		ReleaseDate string `json:"release_date"`
	} `json:"results"`
	TotalPages   int `json:"total_pages"`
	TotalResults int `json:"total_results"`
}

type TMDbMovieDetails struct {
	Title       string `json:"title"`
	ReleaseDate string `json:"release_date"`
	Runtime     int    `json:"runtime"`
	Director    string
	Cast        []string
}

type TMDbPersonDetails struct {
	Name     string `json:"name"`
	Birthday string `json:"birthday"`
}

func connectToDB() (*sql.DB, error) {
	db, err := sql.Open("postgres", connStr)
	if err != nil {
		return nil, err
	}
	if err = db.Ping(); err != nil {
		return nil, err
	}
	fmt.Println("Connected to the database successfully!")
	return db, nil
}

func setupDatabase(db *sql.DB) error {
	// Droping existing tables
	_, err := db.Exec(`
		DROP TABLE IF EXISTS movie_actors, movies, people CASCADE
	`)
	if err != nil {
		return fmt.Errorf("failed to drop existing tables: %w", err)
	}

	// Create people table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS people (
			id SERIAL PRIMARY KEY,
			name VARCHAR(255) NOT NULL UNIQUE,
			birth_year INT
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create people table: %w", err)
	}

	// Create movies table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS movies (
			id SERIAL PRIMARY KEY,
			title VARCHAR(255) NOT NULL,
			director_id INT REFERENCES people(id),
			release_year INT,
			length_minutes INT,
			UNIQUE (title, director_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create movies table: %w", err)
	}

	// Create movie_actors table
	_, err = db.Exec(`
		CREATE TABLE IF NOT EXISTS movie_actors (
			movie_id INT REFERENCES movies(id),
			actor_id INT REFERENCES people(id),
			PRIMARY KEY (movie_id, actor_id)
		)
	`)
	if err != nil {
		return fmt.Errorf("failed to create movie_actors table: %w", err)
	}

	fmt.Println("Database setup completed successfully!")
	return nil
}

func fetchPopularMovies(page int) (*TMDbMovieResult, error) {
	url := fmt.Sprintf("%s/movie/popular?api_key=%s&page=%d", tmdbBaseURL, tmdbAPIKey, page)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var result TMDbMovieResult
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}

func fetchMovieDetails(movieID int) (*TMDbMovieDetails, error) {
	url := fmt.Sprintf("%s/movie/%d?api_key=%s&append_to_response=credits", tmdbBaseURL, movieID, tmdbAPIKey)
	resp, err := http.Get(url)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	var details struct {
		Title       string `json:"title"`
		ReleaseDate string `json:"release_date"`
		Runtime     int    `json:"runtime"`
		Credits     struct {
			Crew []struct {
				Name string `json:"name"`
				Job  string `json:"job"`
			} `json:"crew"`
			Cast []struct {
				Name string `json:"name"`
			} `json:"cast"`
		} `json:"credits"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return nil, err
	}

	// Extract director and cast
	movie := &TMDbMovieDetails{
		Title:       details.Title,
		ReleaseDate: details.ReleaseDate,
		Runtime:     details.Runtime,
	}
	for _, crew := range details.Credits.Crew {
		if crew.Job == "Director" {
			movie.Director = crew.Name
			break
		}
	}
	for _, cast := range details.Credits.Cast {
		movie.Cast = append(movie.Cast, cast.Name)
	}

	return movie, nil
}

func fetchActorBirthYear(actorName string) (int, error) {
	url := fmt.Sprintf("%s/search/person?api_key=%s&query=%s", tmdbBaseURL, tmdbAPIKey, strings.ReplaceAll(actorName, " ", "+"))
	resp, err := http.Get(url)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var searchResult struct {
		Results []struct {
			ID int `json:"id"`
		} `json:"results"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&searchResult); err != nil {
		return 0, err
	}

	if len(searchResult.Results) == 0 {
		return 0, nil
	}

	actorID := searchResult.Results[0].ID
	detailsURL := fmt.Sprintf("%s/person/%d?api_key=%s", tmdbBaseURL, actorID, tmdbAPIKey)
	resp, err = http.Get(detailsURL)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	var details TMDbPersonDetails
	if err := json.NewDecoder(resp.Body).Decode(&details); err != nil {
		return 0, err
	}

	if details.Birthday != "" {
		var year int
		fmt.Sscanf(details.Birthday[:4], "%d", &year)
		return year, nil
	}

	return 0, nil
}

func saveMovieToDB(db *sql.DB, movie *TMDbMovieDetails) error {
	// Parse runtime into minutes
	length := movie.Runtime

	// Parse release year
	year := 0
	if len(movie.ReleaseDate) >= 4 {
		fmt.Sscanf(movie.ReleaseDate[:4], "%d", &year)
	}

	// Insert Director
	var directorID int
	err := db.QueryRow("INSERT INTO people (name) VALUES ($1) ON CONFLICT (name) DO UPDATE SET name=EXCLUDED.name RETURNING id", movie.Director).Scan(&directorID)
	if err != nil {
		return fmt.Errorf("failed to insert director: %w", err)
	}

	// Insert Movie
	var movieID int
	err = db.QueryRow("INSERT INTO movies (title, director_id, release_year, length_minutes) VALUES ($1, $2, $3, $4) RETURNING id",
		movie.Title, directorID, year, length).Scan(&movieID)
	if err != nil {
		return fmt.Errorf("failed to insert movie: %w", err)
	}

	// Insert Actors
	for _, actor := range movie.Cast {
		var actorID int
		var birthYear sql.NullInt64

		year, err := fetchActorBirthYear(actor)
		if err == nil && year != 0 {
			birthYear = sql.NullInt64{Int64: int64(year), Valid: true}
		}

		err = db.QueryRow("INSERT INTO people (name, birth_year) VALUES ($1, $2) ON CONFLICT (name) DO UPDATE SET birth_year=EXCLUDED.birth_year RETURNING id", actor, birthYear).Scan(&actorID)
		if err != nil {
			return fmt.Errorf("failed to insert actor: %w", err)
		}

		_, err = db.Exec("INSERT INTO movie_actors (movie_id, actor_id) VALUES ($1, $2) ON CONFLICT DO NOTHING", movieID, actorID)
		if err != nil {
			return fmt.Errorf("failed to link movie and actor: %w", err)
		}
	}

	fmt.Printf("Successfully added movie: %s\n", movie.Title)
	return nil
}

func populateDatabase(db *sql.DB) {
	// Check the current number of movies in the database
	var currentMovieCount int
	err := db.QueryRow("SELECT COUNT(*) FROM movies").Scan(&currentMovieCount)
	if err != nil {
		log.Fatalf("Error checking movie count: %v", err)
	}

	// If there are already 100 or more movies, skip population
	if currentMovieCount >= 100 {
		fmt.Printf("Database already has %d movies. Skipping population.\n", currentMovieCount)
		return
	}

	moviesToAdd := 100 - currentMovieCount
	moviesAdded := 0
	fmt.Printf("Database has %d movies. Adding %d more to reach 100...\n", currentMovieCount, moviesToAdd)

	for page := 1; moviesAdded < moviesToAdd; page++ {
		result, err := fetchPopularMovies(page)
		if err != nil {
			log.Printf("Error fetching popular movies: %v", err)
			break
		}

		for _, movieBrief := range result.Results {
			if moviesAdded >= moviesToAdd {
				break
			}

			movie, err := fetchMovieDetails(movieBrief.ID)
			if err != nil {
				log.Printf("Error fetching movie details for %s: %v\n", movieBrief.Title, err)
				continue
			}

			err = saveMovieToDB(db, movie)
			if err != nil {
				log.Printf("Error saving movie %s to database: %v\n", movie.Title, err)
			} else {
				moviesAdded++
			}
		}
	}

	fmt.Printf("Successfully added %d movies to the database.\n", moviesAdded)
}

func deletePerson(db *sql.DB) {
	reader := bufio.NewReader(os.Stdin)

	// Prompt for the person's name
	fmt.Print("Enter the name of the person to delete: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)

	if name == "" {
		fmt.Println("Error: Name cannot be empty.")
		return
	}

	// Checking if the person exists in the database
	var personID int
	err := db.QueryRow("SELECT id FROM people WHERE name = $1", name).Scan(&personID)
	if err == sql.ErrNoRows {
		fmt.Printf("Person '%s' not found in the database.\n", name)
		return
	} else if err != nil {
		fmt.Printf("Error checking for person '%s': %v\n", name, err)
		return
	}

	// Checking if the person is a director
	var directorCount int
	err = db.QueryRow("SELECT COUNT(*) FROM movies WHERE director_id = $1", personID).Scan(&directorCount)
	if err != nil {
		fmt.Printf("Error checking director status for '%s': %v\n", name, err)
		return
	}
	if directorCount > 0 {
		fmt.Printf("Cannot delete '%s' as they are a director of one or more movies.\n", name)
		return
	}

	// Fetching movies where the person is an actor
	rows, err := db.Query(`
		SELECT m.title, m.release_year
		FROM movies m
		JOIN movie_actors ma ON m.id = ma.movie_id
		WHERE ma.actor_id = $1
	`, personID)
	if err != nil {
		fmt.Printf("Error fetching movies for actor '%s': %v\n", name, err)
		return
	}
	defer rows.Close()

	var movies []string
	for rows.Next() {
		var title string
		var year int
		if err := rows.Scan(&title, &year); err != nil {
			fmt.Printf("Error scanning movie row for actor '%s': %v\n", name, err)
			return
		}
		movies = append(movies, fmt.Sprintf("%s (%d)", title, year))
	}

	// Deleteing the person and associated references
	tx, err := db.Begin()
	if err != nil {
		fmt.Printf("Error starting transaction: %v\n", err)
		return
	}

	_, err = tx.Exec("DELETE FROM movie_actors WHERE actor_id = $1", personID)
	if err != nil {
		tx.Rollback()
		fmt.Printf("Error deleting references from movie_actors for '%s': %v\n", name, err)
		return
	}

	_, err = tx.Exec("DELETE FROM people WHERE id = $1", personID)
	if err != nil {
		tx.Rollback()
		fmt.Printf("Error deleting person '%s': %v\n", name, err)
		return
	}

	err = tx.Commit()
	if err != nil {
		fmt.Printf("Error committing transaction for '%s': %v\n", name, err)
		return
	}

	// Print confirmation and list of movies
	fmt.Printf("Successfully deleted '%s' from the database.\n", name)
	if len(movies) > 0 {
		fmt.Println("They were removed from the following movies:")
		for _, movie := range movies {
			fmt.Printf("  - %s\n", movie)
		}
	} else {
		fmt.Println("They were not associated with any movies.")
	}
}

func addPerson(db *sql.DB) {
	reader := bufio.NewReader(os.Stdin)

	// Input person's name
	fmt.Print("Enter person's name: ")
	name, _ := reader.ReadString('\n')
	name = strings.TrimSpace(name)

	if name == "" {
		fmt.Println("Error: Name cannot be empty. Please try again.")
		return
	}

	// Input year of birth
	var yearOfBirth int
	for {
		fmt.Print("Enter year of birth (or press Enter to skip): ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "" {
			// User chose to skip entering a year
			yearOfBirth = 0
			break
		}

		_, err := fmt.Sscanf(input, "%d", &yearOfBirth)
		if err != nil || yearOfBirth <= 0 {
			fmt.Println("Error: Invalid year of birth. Please enter a valid number.")
		} else {
			break
		}
	}

	// Insert person into the database
	var personID int
	err := db.QueryRow(
		"INSERT INTO people (name, birth_year) VALUES ($1, $2) ON CONFLICT (name) DO NOTHING RETURNING id",
		name, sql.NullInt64{Int64: int64(yearOfBirth), Valid: yearOfBirth > 0},
	).Scan(&personID)

	if err != nil {
		fmt.Printf("Error inserting person: %v\n", err)
		return
	}

	if personID == 0 {
		fmt.Printf("Person '%s' already exists in the database.\n", name)
	} else {
		fmt.Printf("Successfully added person: %s (Birth Year: %d)\n", name, yearOfBirth)
	}
}

func addMovie(db *sql.DB) {
	reader := bufio.NewReader(os.Stdin)

	// Input movie title
	fmt.Print("Title: ")
	title, _ := reader.ReadString('\n')
	title = strings.TrimSpace(title)

	// Input movie length in hh:mm format
	var lengthMinutes int
	for {
		fmt.Print("Length: ")
		length, _ := reader.ReadString('\n')
		length = strings.TrimSpace(length)

		var hours, minutes int
		_, err := fmt.Sscanf(length, "%02d:%02d", &hours, &minutes)
		if err != nil || hours < 0 || minutes < 0 || minutes >= 60 {
			fmt.Println("- Bad input format (hh:mm), try again!")
		} else {
			lengthMinutes = hours*60 + minutes
			break
		}
	}

	// Input director and validate existence
	var directorID int
	for {
		fmt.Print("Director: ")
		director, _ := reader.ReadString('\n')
		director = strings.TrimSpace(director)

		err := db.QueryRow("SELECT id FROM people WHERE name = $1", director).Scan(&directorID)
		if err != nil {
			fmt.Printf("- We could not find '%s', try again!\n", director)
		} else {
			break
		}
	}

	// Input release year
	fmt.Print("Released in: ")
	var year int
	_, err := fmt.Scan(&year)
	if err != nil || year <= 0 {
		fmt.Println("Invalid release year. Please try again.")
		reader.ReadString('\n')
		return
	}
	reader.ReadString('\n')

	// Input actors line by line
	fmt.Println("Starring: ")
	var actorIDs []int
	for {
		fmt.Print("> ")
		actor, _ := reader.ReadString('\n')
		actor = strings.TrimSpace(actor)

		// Exit condition
		if strings.ToLower(actor) == "exit" {
			break
		}

		// Check actor existence
		var actorID int
		err := db.QueryRow("SELECT id FROM people WHERE name = $1", actor).Scan(&actorID)
		if err != nil {
			fmt.Printf("- We could not find '%s', try again!\n", actor)
		} else {
			actorIDs = append(actorIDs, actorID)
		}
	}

	// Insert the movie into the database
	var movieID int
	err = db.QueryRow(
		"INSERT INTO movies (title, director_id, release_year, length_minutes) VALUES ($1, $2, $3, $4) RETURNING id",
		title, directorID, year, lengthMinutes,
	).Scan(&movieID)
	if err != nil {
		fmt.Printf("Error inserting movie: %v\n", err)
		return
	}

	// Link actors to the movie
	for _, actorID := range actorIDs {
		_, err := db.Exec(
			"INSERT INTO movie_actors (movie_id, actor_id) VALUES ($1, $2) ON CONFLICT DO NOTHING",
			movieID, actorID,
		)
		if err != nil {
			fmt.Printf("Error linking actor (ID %d) to movie: %v\n", actorID, err)
		}
	}

	fmt.Printf("Successfully added movie: %s\n", title)
}

func listMovies(db *sql.DB, titleRegex, directorRegex, actorRegex *regexp.Regexp, orderBy string) {
	query := `
		SELECT
			m.title,
			p.name AS director,
			m.release_year,
			m.length_minutes
		FROM
			movies m
		JOIN
			people p ON m.director_id = p.id
	`

	// Append filters
	var filters []string
	var params []interface{}
	paramIndex := 1

	if titleRegex != nil {
		filters = append(filters, fmt.Sprintf("m.title ~ $%d", paramIndex))
		params = append(params, titleRegex.String())
		paramIndex++
	}
	if directorRegex != nil {
		filters = append(filters, fmt.Sprintf("p.name ~ $%d", paramIndex))
		params = append(params, directorRegex.String())
		paramIndex++
	}
	if actorRegex != nil {
		// Join the movie_actors table to filter by actor
		filters = append(filters, fmt.Sprintf("EXISTS (SELECT 1 FROM movie_actors ma JOIN people pa ON ma.actor_id = pa.id WHERE ma.movie_id = m.id AND pa.name ~ $%d)", paramIndex))
		params = append(params, actorRegex.String())
		paramIndex++
	}

	// Add WHERE clause if filters are present
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}

	// Add ORDER BY clause
	switch orderBy {
	case "length_asc":
		query += " ORDER BY m.length_minutes ASC, m.title"
	case "length_desc":
		query += " ORDER BY m.length_minutes DESC, m.title"
	default:
		query += " ORDER BY m.title"
	}

	rows, err := db.Query(query, params...)
	if err != nil {
		log.Fatal("Error executing query:", err)
	}
	defer rows.Close()

	// Fetch and display movies
	for rows.Next() {
		var title, director string
		var year, length int
		err := rows.Scan(&title, &director, &year, &length)
		if err != nil {
			log.Fatal("Error scanning row:", err)
		}

		// Display movie information
		fmt.Printf("%s by %s in %d, %02d:%02d\n", title, director, year, length/60, length%60)
	}
}

func listMoviesVerbose(db *sql.DB, titleRegex, directorRegex, actorRegex *regexp.Regexp, orderBy string) {
	query := `
		SELECT
			m.title,
			p.name AS director,
			m.release_year,
			m.length_minutes
		FROM
			movies m
		JOIN
			people p ON m.director_id = p.id
	`

	// Append filters
	var filters []string
	var params []interface{}
	paramIndex := 1

	if titleRegex != nil {
		filters = append(filters, fmt.Sprintf("m.title ~ $%d", paramIndex))
		params = append(params, titleRegex.String())
		paramIndex++
	}
	if directorRegex != nil {
		filters = append(filters, fmt.Sprintf("p.name ~ $%d", paramIndex))
		params = append(params, directorRegex.String())
		paramIndex++
	}
	if actorRegex != nil {
		// Join the movie_actors table to filter by actor
		filters = append(filters, fmt.Sprintf("EXISTS (SELECT 1 FROM movie_actors ma JOIN people pa ON ma.actor_id = pa.id WHERE ma.movie_id = m.id AND pa.name ~ $%d)", paramIndex))
		params = append(params, actorRegex.String())
		paramIndex++
	}

	//  WHERE clause if filters are present
	if len(filters) > 0 {
		query += " WHERE " + strings.Join(filters, " AND ")
	}

	//  ORDER BY clause
	switch orderBy {
	case "length_asc":
		query += " ORDER BY m.length_minutes ASC, m.title"
	case "length_desc":
		query += " ORDER BY m.length_minutes DESC, m.title"
	default:
		query += " ORDER BY m.title"
	}

	rows, err := db.Query(query, params...)
	if err != nil {
		log.Fatal("Error executing query:", err)
	}
	defer rows.Close()

	// Fetching and displaying movies
	for rows.Next() {
		var title, director string
		var year, length int
		err := rows.Scan(&title, &director, &year, &length)
		if err != nil {
			log.Fatal("Error scanning row:", err)
		}

		// Display movie information
		fmt.Printf("%s by %s in %d, %02d:%02d\n", title, director, year, length/60, length%60)
		fmt.Println("    Starring:")

		// Fetch and display actors for this movie
		displayActorsForMovie(db, title, year, actorRegex)
	}
}

func displayActorsForMovie(db *sql.DB, movieTitle string, releaseYear int, actorRegex *regexp.Regexp) {
	query := `
		SELECT
			p.name,
			COALESCE(EXTRACT(YEAR FROM age(DATE ($1 || '-01-01'), TO_DATE(p.birth_year::TEXT, 'YYYY'))), 0) AS age
		FROM
			people p
		JOIN
			movie_actors ma ON p.id = ma.actor_id
		JOIN
			movies m ON ma.movie_id = m.id
		WHERE
			m.title = $2
	`

	var rows *sql.Rows
	var err error

	// Checking if actorRegex is provided
	if actorRegex != nil {
		query += " AND p.name ~ $3"
		rows, err = db.Query(query, releaseYear, movieTitle, actorRegex.String())
	} else {
		rows, err = db.Query(query, releaseYear, movieTitle)
	}

	if err != nil {
		log.Fatal("Error fetching actors for movie:", err)
	}
	defer rows.Close()

	for rows.Next() {
		var actor string
		var age int
		err := rows.Scan(&actor, &age)
		if err != nil {
			log.Fatal("Error scanning actor row:", err)
		}

		// Handle missing birth_year (age = 0)
		if age == 0 {
			fmt.Printf("        - %s (birth year missing)\n", actor)
		} else {
			fmt.Printf("        - %s at age %d\n", actor, age)
		}
	}
}

// Helper function to manually parse arguments, handling quoted strings correctly.
func parseArgs(input string) []string {
	var args []string
	var currentArg strings.Builder
	inQuotes := false

	// Iterate over each character in the input string
	for _, char := range input {
		switch char {
		case ' ':
			if inQuotes {
				// Inside quotes, so space is part of the argument
				currentArg.WriteRune(char)
			} else if currentArg.Len() > 0 {
				// End of an argument, add it to the list
				args = append(args, currentArg.String())
				currentArg.Reset()
			}
		case '"':
			// Toggle the inQuotes flag
			inQuotes = !inQuotes
		default:
			// Add character to the current argument
			currentArg.WriteRune(char)
		}
	}

	// Add the last argument if exists
	if currentArg.Len() > 0 {
		args = append(args, currentArg.String())
	}

	return args
}

func parseCommand(input string, db *sql.DB) {
	// Custom function to parse arguments correctly
	args := parseArgs(input)

	if len(args) == 0 {
		fmt.Println("Invalid command. Please enter a valid command.")
		return
	}

	command := args[0]
	switch command {
	case "l": // List movies
		verbose := false
		var titleRegex, directorRegex, actorRegex *regexp.Regexp
		orderBy := "title"

		// Parse flags
		for i := 1; i < len(args); i++ {
			switch args[i] {
			case "-v":
				verbose = true
			case "-t":
				if i+1 < len(args) {
					titleName := args[i+1]
					if strings.HasPrefix(titleName, "\"") && strings.HasSuffix(titleName, "\"") {
						// Remove the surrounding quotes
						titleName = strings.Trim(titleName, "\"")
					}
					titleRegex = regexp.MustCompile(titleName)
					i++ // Skip the next argument since it's part of the title
				} else {
					fmt.Println("Error: Missing regex for -t.")
					return
				}
			case "-d":
				if i+1 < len(args) {

					directorName := args[i+1]
					if strings.HasPrefix(directorName, "\"") && strings.HasSuffix(directorName, "\"") {
						directorName = strings.Trim(directorName, "\"")
					}
					directorRegex = regexp.MustCompile(directorName)
					i++
				} else {
					fmt.Println("Error: Missing director name for -d.")
					return
				}
			case "-a":
				if i+1 < len(args) {
					actorName := args[i+1]
					if strings.HasPrefix(actorName, "\"") && strings.HasSuffix(actorName, "\"") {
						actorName = strings.Trim(actorName, "\"")
					}
					actorRegex = regexp.MustCompile(actorName)
					i++
				} else {
					fmt.Println("Error: Missing regex for -a.")
					return
				}
			case "-la":
				if orderBy == "length_desc" {
					fmt.Println("Error: Both -la and -ld cannot be used together.")
					return
				}
				orderBy = "length_asc"
			case "-ld":
				if orderBy == "length_asc" {
					fmt.Println("Error: Both -la and -ld cannot be used together.")
					return
				}
				orderBy = "length_desc"
			default:
				fmt.Printf("Unknown flag: %s\n", args[i])
				return
			}
		}

		if verbose {
			listMoviesVerbose(db, titleRegex, directorRegex, actorRegex, orderBy)
		} else {
			listMovies(db, titleRegex, directorRegex, actorRegex, orderBy)
		}

	case "a": // Add
		if len(args) > 1 {
			if args[1] == "-p" {
				addPerson(db)
			} else if args[1] == "-m" {
				addMovie(db)
			} else {
				fmt.Println("Unknown or unsupported 'a' command.")
			}
		} else {
			fmt.Println("Unknown or unsupported 'a' command.")
		}

	case "d": // Delete
		if len(args) > 1 {
			if args[1] == "-p" {
				deletePerson(db)
			} else {
				fmt.Println("Unknown or unsupported 'd' command.")
			}
		} else {
			fmt.Println("Unknown or unsupported 'd' command.")
		}

	default:
		fmt.Printf("Unknown command: %s\n", command)
	}
}

func main() {
	db, err := connectToDB()
	if err != nil {
		log.Fatal("Error connecting to database:", err)
	}
	defer db.Close()

	err = setupDatabase(db)
	if err != nil {
		log.Fatal("Database setup failed:", err)
	}

	// Populate the database
	populateDatabase(db)

	fmt.Println("Welcome to the Movie Console Application!")
	reader := bufio.NewReader(os.Stdin)

	for {
		fmt.Print("> ")
		input, _ := reader.ReadString('\n')
		input = strings.TrimSpace(input)

		if input == "exit" {
			fmt.Println("Goodbye!")
			break
		}

		parseCommand(input, db)
	}
}
