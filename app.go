package main

import (
	"context"
	"errors"

	"github.com/AVMG20/gomail/internal/models"
)

var errNotImplemented = errors.New("not implemented")

// App is the Wails-bound facade. Every exported method is callable from the
// Vue frontend via the generated bindings in frontend/wailsjs/go/main/App.
// Keep this layer thin: it delegates to the services in internal/.
type App struct {
	ctx context.Context
}

func NewApp() *App { return &App{} }

func (a *App) startup(ctx context.Context) { a.ctx = ctx }

func (a *App) shutdown(ctx context.Context) {}

// ---- Accounts ----

func (a *App) GetAccounts() ([]models.Account, error) { return []models.Account{}, nil }

// AddAccount runs the OAuth flow for provider ("gmail" | "outlook") in the
// system browser and blocks until it completes, is cancelled, or times out.
func (a *App) AddAccount(provider string) (models.Account, error) {
	return models.Account{}, errNotImplemented
}
func (a *App) CancelAddAccount()                                        {}
func (a *App) RemoveAccount(accountID string) error                     { return errNotImplemented }
func (a *App) UpdateAccount(accountID, displayName, color string) error { return errNotImplemented }

// ---- Folders & messages ----

func (a *App) GetFolders(accountID string) ([]models.Folder, error) { return []models.Folder{}, nil }
func (a *App) ListMessages(q models.ListQuery) (models.MessagePage, error) {
	return models.MessagePage{Messages: []models.MessageSummary{}}, nil
}
func (a *App) GetMessage(id string) (models.Message, error) {
	return models.Message{}, errNotImplemented
}
func (a *App) GetThread(id string) ([]models.Message, error)       { return nil, errNotImplemented }
func (a *App) MarkRead(ids []string, read bool) error              { return errNotImplemented }
func (a *App) SetStarred(ids []string, starred bool) error         { return errNotImplemented }
func (a *App) Archive(ids []string) error                          { return errNotImplemented }
func (a *App) Trash(ids []string) error                            { return errNotImplemented }
func (a *App) MoveToFolder(ids []string, folderID string) error    { return errNotImplemented }
func (a *App) SendMessage(req models.ComposeRequest) error         { return errNotImplemented }
func (a *App) SaveAttachment(messageID, attachmentID string) error { return errNotImplemented }
func (a *App) SyncNow(accountID string) error                      { return errNotImplemented }
func (a *App) OpenExternalURL(url string)                          {}

// ---- Settings ----

func (a *App) GetSettings() (models.Settings, error) { return models.Settings{}, nil }
func (a *App) SaveSettings(s models.Settings) error  { return errNotImplemented }
func (a *App) SetOpenRouterKey(key string) error     { return errNotImplemented }

// ---- AI (OpenRouter) ----

func (a *App) TestOpenRouter() error                   { return errNotImplemented }
func (a *App) ListAIModels() ([]models.AIModel, error) { return []models.AIModel{}, nil }
func (a *App) ClassifyMessages(ids []string) ([]models.ClassificationResult, error) {
	return nil, errNotImplemented
}
func (a *App) ClassifyFolder(q models.ListQuery) (int, error)     { return 0, errNotImplemented }
func (a *App) SetCategory(ids []string, categoryID string) error  { return errNotImplemented }
func (a *App) SummarizeMessage(id string) (string, error)         { return "", errNotImplemented }
func (a *App) DraftReply(id, instructions string) (string, error) { return "", errNotImplemented }
