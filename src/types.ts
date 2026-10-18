export type ArchiveAction = "create" | "extract" | "test";

export type ArchiveRecord = {
  path: string;
  name: string;
  format: string;
  size: number;
  modified: string;
  action: ArchiveAction;
  result: "success" | "error";
};

export type ArchiveItem = {
  name: string;
  size: number | null;
  packed_size: number | null;
  is_directory: boolean;
  modified: string | null;
};

export type ToolResult = {
  ok: boolean;
  code: number | null;
  stdout: string;
  stderr: string;
};

export type LaunchRequest = {
  action: string;
  path: string | null;
};

export type ShellIntegrationStatus = {
  enabled: boolean;
  healthy: boolean;
};
