# Petit à petit

A local French vocabulary app built with Go, HTMX and SQLite.

## Run

Requires Go 1.23+ and a C compiler (the SQLite driver uses CGO).

```sh
go run .
```

Open http://localhost:8080. Run commands from this directory. No Node.js or frontend build step is needed; HTMX is served locally.

Add English/French words or phrases, pick a translation direction, type your answer, and check it. Answers ignore case, whitespace and straight versus curly apostrophes, but require correct accents. Separate alternative translations with semicolons in either language: for example, English `well; good` and French `bien` accept either `well` or `good` when translating from French. Enter any one translation as your answer. The first alternative is shown when that language is used as the question; feedback shows all accepted alternatives. Commas remain part of phrases. Existing vocabulary and practice history continue to work without a database migration. Remove unwanted words from the vocabulary table. Data and practice totals persist in `french.db` across restarts. Removing a word also removes its practice history.

Optional environment variables: `PORT` (default `8080`) and `DATABASE_PATH` (default `french.db`). The app binds to loopback and is intended for personal local use, without user accounts.

```sh
go test ./...
go vet ./...
```
