export type RdesktopPushMessage = {
  cmd?: string;
  payload?: Record<string, unknown>;
};

declare global {
  interface Window {
    __RDESKTOP_INVOKE__?: (
      command: string,
      payload?: Record<string, unknown>,
      options?: { timeoutMs?: number },
    ) => Promise<{ success: boolean; data?: unknown }>;
    __RDESKTOP_PUSH__?: (message: RdesktopPushMessage) => void;
  }
}

export function hasDesktopBridge(): boolean {
  return typeof window !== "undefined" && typeof window.__RDESKTOP_INVOKE__ === "function";
}

export async function invoke<T>(command: string, payload: Record<string, unknown> = {}): Promise<T> {
  const bridge = window.__RDESKTOP_INVOKE__;
  if (typeof bridge !== "function") throw new Error("当前页面没有连接 zip233 桌面运行时。");
  const response = await bridge(command, payload);
  if (!response?.success) {
    const data = response?.data as { error?: unknown } | undefined;
    throw new Error(typeof data?.error === "string" ? data.error : `zip233 操作失败：${command}`);
  }
  return response.data as T;
}
