package app

import (
	"context"
	"fmt"
	"time"

	"poople/ladder"
	"poople/store"
	"poople/store/zz"

	"zombiezen.com/go/sqlite"
)

// LoadWords stores data as a new word-list version unless its checksum is
// already stored, and returns the version's id. A new version becomes current;
// older versions stay intact for the puzzles that froze them.
func LoadWords(ctx context.Context, db *store.Store, data *ladder.Data) (listID int64, err error) {
	checksum := data.Checksum()
	if err := db.Read(ctx, func(conn *sqlite.Conn) (err error) {
		listID, err = zz.OnceFindWordList(conn, checksum)
		return err
	}); err != nil || listID != 0 {
		return listID, err
	}
	words, calendar := data.Words(), data.Calendar()
	err = db.Write(ctx, func(conn *sqlite.Conn) (err error) {
		// Another process may have stored the same data since the read.
		if listID, err = zz.OnceFindWordList(conn, checksum); err != nil || listID != 0 {
			return err
		}
		if listID, err = zz.OnceInsertWordList(conn, zz.InsertWordListParams{Checksum: checksum, CreatedAt: time.Now().UnixMilli()}); err != nil {
			return err
		}
		insertWord := zz.InsertWord(conn)
		for _, word := range words {
			row := zz.InsertWordParams{ListId: listID, Word: word}
			if dist, ok := data.Distance(word); ok {
				distance := int64(dist)
				row.Distance = &distance
			}
			if data.Common(word) {
				row.Common = 1
			}
			if err := insertWord.Run(row); err != nil {
				return fmt.Errorf("insert word %q: %w", word, err)
			}
		}
		insertEntry := zz.InsertCalendarEntry(conn)
		for position, word := range calendar {
			if err := insertEntry.Run(zz.InsertCalendarEntryParams{ListId: listID, Position: int64(position), Word: word}); err != nil {
				return fmt.Errorf("insert calendar entry %d: %w", position, err)
			}
		}
		return nil
	})
	return listID, err
}
