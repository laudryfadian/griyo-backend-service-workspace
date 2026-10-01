CREATE SCHEMA IF NOT EXISTS workspace;
CREATE TABLE IF NOT EXISTS workspace.projects(id text PRIMARY KEY,data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.agents(id text PRIMARY KEY,project_id text REFERENCES workspace.projects(id),data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.documents(id text PRIMARY KEY,project_id text NOT NULL REFERENCES workspace.projects(id),data jsonb NOT NULL);
CREATE TABLE IF NOT EXISTS workspace.settings(id text PRIMARY KEY,data jsonb NOT NULL);
