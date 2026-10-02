"use client";

import { useEffect, useRef } from "react";
import { useNavigation } from "../../navigation";
import { parseCurrentContextRoute } from "./use-chat-context-items";

/** Seed a draft at entry time, without changing saved sessions or user edits. */
export function useChatDraftProjectContext(
  isOpen: boolean,
  activeSessionId: string | null,
  setSelectedProjectId: (id: string | null) => void,
) {
  const { pathname, searchParams } = useNavigation();
  const context = parseCurrentContextRoute(pathname, searchParams);
  const projectId = context?.type === "project" ? context.id : null;
  const wasOpen = useRef(false);

  useEffect(() => {
    const enteringDraft = isOpen && !wasOpen.current && !activeSessionId;
    wasOpen.current = isOpen;
    // Session changes can also come from an explicit project selection. Only
    // opening the window seeds here; New chat applies the route in its handler.
    if (enteringDraft && projectId) setSelectedProjectId(projectId);
  }, [isOpen, activeSessionId, projectId, setSelectedProjectId]);

  return projectId;
}
