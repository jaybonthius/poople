-- name: GetAppMeta :one
SELECT value FROM app_meta WHERE key = @key;
