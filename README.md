# Petit à petit

A local French vocabulary app built with Go, HTMX and SQLite.

## Run

Requires Go 1.23+ and a C compiler (the SQLite driver uses CGO).

```sh
go run .
```

Open http://localhost:8080. Run commands from this directory. No Node.js or frontend build step is needed; HTMX is served locally.

Add English/French words or phrases, pick a translation direction, type your answer, and check it. Answers ignore case, whitespace and straight versus curly apostrophes, but require correct accents. Separate alternative translations with semicolons in either language: for example, English `well; good` and French `bien` accept either `well` or `good` when translating from French. Enter any one translation as your answer. The first alternative is shown when that language is used as the question; feedback shows all accepted alternatives. Commas remain part of phrases. Existing vocabulary and practice history are preserved. Remove unwanted words from the vocabulary table. Data and practice totals persist in `french.db` across restarts. Removing a word also removes its practice history.

Optional environment variables: `PORT` (default `8080`) and `DATABASE_PATH` (default `french.db`). The app binds to loopback and is intended for personal local use, without user accounts.

```sh
go test ./...
go vet ./...
```

## Vocabulary groups

When adding a word, type an optional free-text group (for example, Clothing, Greetings or Food), or choose an existing group from the input suggestions. Group names ignore case and extra spaces when reusing an existing group. Use Practice group and New card to practise all words, a single group, or ungrouped words. The vocabulary table follows the same filter; overall practice totals remain global. Use each word’s Edit button to group or move existing words. Clear the Group field to make a word ungrouped. Groups are listed while they contain words. Existing words become ungrouped automatically, without losing translations or practice history.

## Noun gender

Choose the optional French noun gender when adding a word: Masculine, Feminine, or Not specified / not a noun. The saved gender appears on flashcards and in the vocabulary table, where it can be changed or cleared in the Edit dialog. Gender is a learning hint; translation checking still uses the translations you entered, including any articles. Existing words start with no gender specified, and the automatic database update preserves groups and practice history.

## Editing words

Click Edit beside a vocabulary word to open its editor. Change English/French translations (including semicolon-separated alternatives), group, and French noun gender, then use Save changes. Cancel, the close button, or Escape dismiss the editor without saving. Validation errors keep the editor open with your entries intact. Updates preserve the word’s practice history and your current practice group and direction.

## Rules notebook

Open Rules notebook in the navigation to keep explanations separate from vocabulary practice. Add a title and explanation, with optional correct/incorrect examples, a personal reminder, and a topic. Type new topic names or reuse suggestions. Click a rule heading to expand it; Edit opens the same style of popup used for vocabulary. Cancel or Escape closes the editor without saving. Search covers titles, explanations, examples, reminders, and topics, and can be combined with a topic filter. Remove asks for confirmation. Rules persist in a separate SQLite table and do not count toward flashcard practice or scores.
