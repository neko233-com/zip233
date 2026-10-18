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

export type UpdateInfo = {
  current_version: string;
  latest_version: string | null;
  available: boolean;
  release_url: string | null;
  release_notes: string | null;
  asset_name: string | null;
  asset_size: number | null;
  can_download: boolean;
  message: string;
};

export type UpdateDownloadState = {
  status: "idle" | "downloading" | "ready" | "error" | string;
  version: string | null;
  asset_name: string | null;
  downloaded_bytes: number;
  total_bytes: number | null;
  message: string;
  file_path: string | null;
};
