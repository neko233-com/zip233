pub mod archive;
pub mod update;

use serde::Serialize;

#[derive(Serialize)]
pub struct ToolResult {
    pub ok: bool,
    pub code: Option<i32>,
    pub stdout: String,
    pub stderr: String,
}

#[derive(Serialize)]
pub struct ArchiveItem {
    pub name: String,
    pub size: Option<u64>,
    pub packed_size: Option<u64>,
    pub is_directory: bool,
    pub modified: Option<String>,
}
