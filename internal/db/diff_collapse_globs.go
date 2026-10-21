package db

import (
	"context"
	"database/sql"
)

// GetDiffCollapseGlobs returns the saved collapse globs for a repo in
// saved order. Returns an empty (non-nil) slice when none are saved.
func (d *DB) GetDiffCollapseGlobs(ctx context.Context, repoID int64) ([]string, error) {
	rows, err := d.ro.QueryContext(ctx,
		`SELECT glob FROM middleman_repo_diff_collapse_globs
		  WHERE repo_id = ? ORDER BY position ASC`,
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	globs := []string{}
	for rows.Next() {
		var g string
		if err := rows.Scan(&g); err != nil {
			return nil, err
		}
		globs = append(globs, g)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return globs, nil
}

// SetDiffCollapseGlobs replaces the saved collapse globs for a repo,
// preserving the given order. Runs in a transaction: deletes the
// existing rows then inserts the new list.
func (d *DB) SetDiffCollapseGlobs(ctx context.Context, repoID int64, globs []string) error {
	return d.Tx(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx,
			`DELETE FROM middleman_repo_diff_collapse_globs WHERE repo_id = ?`,
			repoID,
		); err != nil {
			return err
		}
		for i, g := range globs {
			if _, err := tx.ExecContext(ctx,
				`INSERT INTO middleman_repo_diff_collapse_globs (repo_id, position, glob)
				 VALUES (?, ?, ?)`,
				repoID, i, g,
			); err != nil {
				return err
			}
		}
		return nil
	})
}
