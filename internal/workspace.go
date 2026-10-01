package internal

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/laudryfadian/griyo-backend-service-workspace/internal/domain"
	"github.com/laudryfadian/griyo-backend-service-workspace/internal/pb"
	"github.com/laudryfadian/griyo-backend-service-workspace/internal/platform"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"strings"
	"time"
)

const migration = `CREATE SCHEMA IF NOT EXISTS workspace;
CREATE TABLE IF NOT EXISTS workspace.projects(id text PRIMARY KEY,data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.agents(id text PRIMARY KEY,project_id text REFERENCES workspace.projects(id),data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.documents(id text PRIMARY KEY,project_id text NOT NULL REFERENCES workspace.projects(id),data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.settings(id text PRIMARY KEY,data jsonb NOT NULL);`

type Service struct {
	pb.UnimplementedWorkspaceServiceServer
	db    *pgxpool.Pool
	tasks pb.TaskServiceClient
}

func New(ctx context.Context, db *pgxpool.Pool, tasks pb.TaskServiceClient) (*Service, error) {
	_, e := db.Exec(ctx, migration)
	return &Service{db: db, tasks: tasks}, e
}
func (s *Service) Execute(ctx context.Context, r *pb.Request) (*pb.Response, error) {
	table := ""
	switch r.Resource {
	case "projects":
		table = "workspace.projects"
	case "agents":
		table = "workspace.agents"
	case "documents":
		table = "workspace.documents"
	case "settings":
		table = "workspace.settings"
	default:
		return nil, platform.Invalid("unknown resource")
	}
	if r.Operation == "list" {
		rows, e := s.db.Query(ctx, "SELECT data FROM "+table+" ORDER BY id")
		if e != nil {
			return nil, platform.Internal(e)
		}
		defer rows.Close()
		items := []json.RawMessage{}
		for rows.Next() {
			var b json.RawMessage
			if e = rows.Scan(&b); e != nil {
				return nil, platform.Internal(e)
			}
			items = append(items, b)
		}
		if e = rows.Err(); e != nil {
			return nil, platform.Internal(e)
		}
		return platform.Reply(items)
	}
	if r.Operation == "get" {
		var b json.RawMessage
		e := s.db.QueryRow(ctx, "SELECT data FROM "+table+" WHERE id=$1", r.Id).Scan(&b)
		if errors.Is(e, pgx.ErrNoRows) {
			if r.Resource == "settings" {
				return platform.Reply(domain.Settings{WorkspaceName: "Griyo", Timezone: "Asia/Jakarta", DailyBudget: 10, MonthlyBudget: 100})
			}
			return nil, platform.NotFound()
		}
		if e != nil {
			return nil, platform.Internal(e)
		}
		return &pb.Response{Body: b}, nil
	}
	if r.Operation != "create" && r.Operation != "update" {
		return nil, platform.Invalid("unsupported operation")
	}
	id := r.Id
	if r.Operation == "create" {
		id = platform.Id()
	}
	var body []byte
	var projectId *string
	switch r.Resource {
	case "projects":
		var p domain.Project
		if e := platform.Decode(r.Body, &p); e != nil {
			return nil, e
		}
		if e := platform.ValidateName(p.Name); e != nil {
			return nil, e
		}
		if p.WorkServerId == "" || p.DeployServerId == "" {
			return nil, platform.Invalid("server mapping is required")
		}
		if p.RepositoryUrl != "" && (!strings.HasPrefix(p.RepositoryUrl, "https://") || strings.Contains(p.RepositoryUrl, "@")) {
			return nil, platform.Invalid("repositoryUrl must be credential-free HTTPS URL")
		}
		p.Id = id
		p.CreatedAt = time.Now().UTC()
		if r.Operation == "update" {
			var oldBytes []byte
			if e := s.db.QueryRow(ctx, "SELECT data FROM workspace.projects WHERE id=$1", id).Scan(&oldBytes); e != nil {
				return nil, platform.NotFound()
			}
			var old domain.Project
			if e := json.Unmarshal(oldBytes, &old); e != nil {
				return nil, platform.Internal(e)
			}
			p.CreatedAt = old.CreatedAt
		}
		body, _ = json.Marshal(p)
	case "agents":
		var a domain.Agent
		if e := platform.Decode(r.Body, &a); e != nil {
			return nil, e
		}
		if e := platform.ValidateName(a.Name); e != nil {
			return nil, e
		}
		if strings.TrimSpace(a.Role) == "" {
			return nil, platform.Invalid("role is required")
		}
		if a.ProjectId != nil && *a.ProjectId == "" {
			a.ProjectId = nil
		}
		if a.Skills == nil {
			a.Skills = []string{}
		}
		if a.Status == "" {
			a.Status = "Available"
		}
		if a.Status != "Available" && a.Status != "Paused" {
			return nil, platform.Invalid("agent status must be Available or Paused")
		}
		if r.Operation == "update" {
			var oldBody []byte
			e := s.db.QueryRow(ctx, "SELECT data FROM workspace.agents WHERE id=$1", id).Scan(&oldBody)
			if e != nil {
				return nil, platform.NotFound()
			}
			var old domain.Agent
			if e = json.Unmarshal(oldBody, &old); e != nil {
				return nil, platform.Internal(e)
			}
			oldProject := ""
			newProject := ""
			if old.ProjectId != nil {
				oldProject = *old.ProjectId
			}
			if a.ProjectId != nil {
				newProject = *a.ProjectId
			}
			if oldProject != newProject {
				res, e := s.tasks.Execute(ctx, platform.Request("hasActive", "tasks", id, nil))
				if e != nil {
					return nil, e
				}
				var active bool
				if e = json.Unmarshal(res.Body, &active); e != nil {
					return nil, platform.Internal(e)
				}
				if active {
					return nil, platform.Conflict("agent has active tasks; finish or requeue them before changing project")
				}
			}
		}
		a.Id = id
		projectId = a.ProjectId
		body, _ = json.Marshal(a)
	case "documents":
		var d domain.Document
		if e := platform.Decode(r.Body, &d); e != nil {
			return nil, e
		}
		if e := platform.ValidateName(d.Title); e != nil {
			return nil, e
		}
		if d.ProjectId == "" || d.Body == "" {
			return nil, platform.Invalid("projectId and body required")
		}
		d.Id = id
		projectId = &d.ProjectId
		body, _ = json.Marshal(d)
	case "settings":
		var v domain.Settings
		if e := platform.Decode(r.Body, &v); e != nil {
			return nil, e
		}
		if v.DailyBudget <= 0 || v.MonthlyBudget <= 0 {
			return nil, platform.Invalid("budgets must be positive")
		}
		id = "workspace"
		body, _ = json.Marshal(v)
	}
	query := "INSERT INTO " + table + "(id,data) VALUES($1,$2)"
	args := []interface{}{id, body}
	if r.Resource == "agents" || r.Resource == "documents" {
		query = "INSERT INTO " + table + "(id,data,project_id) VALUES($1,$2,$3)"
		args = append(args, projectId)
	}
	if r.Operation == "update" && r.Resource != "settings" {
		query = "UPDATE " + table + " SET data=$2 WHERE id=$1"
		if projectId != nil || r.Resource == "agents" {
			query = "UPDATE " + table + " SET data=$2,project_id=$3 WHERE id=$1"
			args = []interface{}{id, body, projectId}
		}
	}
	if r.Resource == "settings" {
		query += " ON CONFLICT(id) DO UPDATE SET data=EXCLUDED.data"
	}
	tag, e := s.db.Exec(ctx, query, args...)
	if e != nil {
		if strings.Contains(e.Error(), "23503") {
			return nil, status.Error(codes.InvalidArgument, "project does not exist")
		}
		return nil, platform.Internal(e)
	}
	if tag.RowsAffected() == 0 {
		return nil, platform.NotFound()
	}
	return &pb.Response{Body: body}, nil
}
