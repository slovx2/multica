"use client";

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Check, ListChecks, SlidersHorizontal } from "lucide-react";
import { toast } from "sonner";
import { api } from "@multica/core/api";
import { useWorkspaceId } from "@multica/core/hooks";
import { useChatStore } from "@multica/core/chat";
import { chatKeys } from "@multica/core/chat/queries";
import type { ChatExecutionOverrides } from "@multica/core/types/chat";
import {
  runtimeListOptions,
  runtimeModelsOptions,
} from "@multica/core/runtimes";
import {
  DropdownMenu,
  DropdownMenuTrigger,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSub,
  DropdownMenuSubContent,
  DropdownMenuSubTrigger,
} from "@multica/ui/components/ui/dropdown-menu";
import { ClearablePillButton } from "../../common/pill-button";
import { pickModelEntry } from "../../agents/components/inspector/thinking-prop-row";
import { useT } from "../../i18n";

interface SettingsProps {
  sessionId?: string | null;
  runtimeId?: string;
  model?: string;
  disabled?: boolean;
  enabled: boolean;
}

export function useChatSessionSettings({
  enabled,
  sessionId,
  runtimeId,
  model,
  disabled,
}: SettingsProps) {
  const { t: tAgents } = useT("agents");
  const wsId = useWorkspaceId();
  const qc = useQueryClient();
  const runtimes = useQuery({ ...runtimeListOptions(wsId), enabled });
  const runtime = enabled
    ? runtimes.data?.find((r) => r.id === runtimeId)
    : undefined;
  const catalog = useQuery(
    runtimeModelsOptions(runtime?.status === "online" ? runtimeId : null),
  );
  const provider = runtime?.provider ?? "";
  const entry = pickModelEntry(
    catalog.data?.models ?? [],
    model ?? "",
    provider,
  );
  const levels = entry?.thinking?.supported_levels ?? [];
  const supportsExplicitStandard = catalog.data?.models.some(
    (candidate) => candidate.supports_explicit_standard_service_tier === true,
  );
  const tiers =
    provider === "codex" &&
    entry?.id.startsWith("gpt-") &&
    entry.service_tiers?.length
      ? [
          ...(supportsExplicitStandard
            ? [
                {
                  id: "default",
                  name: tAgents(($) => $.pickers.service_tier_standard),
                },
              ]
            : []),
          ...entry.service_tiers.filter((tier) => tier.id !== "default"),
        ]
      : [];
  const draftPlanMode = useChatStore((s) => s.draftPlanMode);
  const draftOverrides = useChatStore((s) => s.draftExecutionOverrides);
  const session = useQuery({
    queryKey: chatKeys.session(wsId, sessionId ?? ""),
    queryFn: () => api.getChatSession(sessionId!),
    enabled: enabled && !!sessionId,
  });
  const update = useMutation({
    mutationFn: (
      data:
        | { plan_mode: boolean }
        | { execution_overrides: ChatExecutionOverrides },
    ) => api.updateChatSession(sessionId!, data),
    onSuccess: (value) => {
      qc.setQueryData(chatKeys.session(wsId, value.id), value);
      void qc.invalidateQueries({ queryKey: chatKeys.all(wsId) });
    },
    onError: (error: Error) => toast.error(error.message),
  });
  if (!enabled) return null;
  return {
    plan: sessionId ? session.data?.plan_mode === true : draftPlanMode,
    overrides:
      (sessionId ? session.data?.execution_overrides : draftOverrides) ?? {},
    levels,
    tiers,
    contextState:
      session.data?.context_state?.runtime_id &&
      session.data.context_state.runtime_id !== runtimeId
        ? undefined
        : session.data?.context_state,
    provider,
    sessionId,
    supportsPlan: provider === "claude" || provider === "codex",
    disabled: disabled || update.isPending || (!!sessionId && !session.data),
    setPlan: (plan: boolean) =>
      sessionId
        ? update.mutate({ plan_mode: plan })
        : useChatStore.getState().setDraftPlanMode(plan),
    setOverrides: (overrides: ChatExecutionOverrides) =>
      sessionId
        ? update.mutate({ execution_overrides: overrides })
        : useChatStore.getState().setDraftExecutionOverrides(overrides),
  };
}

type ChatSettingsState = NonNullable<ReturnType<typeof useChatSessionSettings>>;

export function ChatSettingsMenu({
  settings,
  includePlan = true,
  onCompact,
}: {
  settings: ChatSettingsState | null;
  includePlan?: boolean;
  onCompact?: () => void;
}) {
  const { t } = useT("chat");
  if (!settings) return null;
  const { plan, overrides, levels, tiers, disabled, setPlan, setOverrides } =
    settings;
  const choices = [
    {
      field: "thinking_level" as const,
      label: t(($) => $.execution.thinking),
      options: levels.map((level) => ({
        value: level.value,
        label: level.label,
      })),
    },
    {
      field: "service_tier" as const,
      label: t(($) => $.execution.tier),
      options: tiers.map((tier) => ({ value: tier.id, label: tier.name })),
    },
  ];
  return (
    <>
      {onCompact && (
        <DropdownMenuItem
          disabled={disabled || !settings.sessionId || !settings.supportsPlan}
          title={
            !settings.supportsPlan ? t(($) => $.context.unsupported) : undefined
          }
          onClick={onCompact}
        >
          {t(($) => $.context.compact)}
        </DropdownMenuItem>
      )}
      {includePlan && (
        <DropdownMenuItem
          disabled={disabled || (!plan && !settings.supportsPlan)}
          onClick={() => setPlan(!plan)}
        >
          <ListChecks />
          {t(($) => $.planning.mode)}
          {plan && <Check className="ml-auto" />}
        </DropdownMenuItem>
      )}
      {choices
        .filter((choice) => choice.options.length > 0)
        .map(({ field, label, options }) => (
          <DropdownMenuSub key={field}>
            <DropdownMenuSubTrigger
              disabled={disabled}
              className="data-disabled:pointer-events-none data-disabled:opacity-50"
            >
              {label}
            </DropdownMenuSubTrigger>
            <DropdownMenuSubContent>
              {[
                { value: "", label: t(($) => $.execution.inherit) },
                ...options,
              ].map((option) => (
                <DropdownMenuItem
                  key={option.value}
                  onClick={() =>
                    setOverrides({ ...overrides, [field]: option.value })
                  }
                >
                  {option.label}
                  {(overrides[field] ?? "") === option.value && (
                    <Check className="ml-auto" />
                  )}
                </DropdownMenuItem>
              ))}
            </DropdownMenuSubContent>
          </DropdownMenuSub>
        ))}
      {(overrides.thinking_level || overrides.service_tier) && (
        <DropdownMenuItem disabled={disabled} onClick={() => setOverrides({})}>
          {t(($) => $.execution.clear)}
        </DropdownMenuItem>
      )}
    </>
  );
}

export function ChatSettingsTags({
  settings,
}: {
  settings: ChatSettingsState | null;
}) {
  const { t } = useT("chat");
  if (!settings) return null;
  const { plan, overrides, tiers, disabled, setPlan, setOverrides } = settings;
  const label = [
    overrides.thinking_level,
    tiers.find((tier) => tier.id === overrides.service_tier)?.name ??
      overrides.service_tier,
  ]
    .filter(Boolean)
    .join(" · ");
  return (
    <>
      {plan && (
        <ClearablePillButton
          className="h-6 shrink-0 border-brand text-brand"
          disabled={disabled}
          onClear={() => setPlan(false)}
          clearLabel={t(($) => $.execution.exit_plan)}
        >
          {t(($) => $.planning.mode)}
        </ClearablePillButton>
      )}
      {label && (
        <DropdownMenu>
          <DropdownMenuTrigger
            render={
              <ClearablePillButton
                className="h-6"
                disabled={disabled}
                onClear={() => setOverrides({})}
                clearLabel={t(($) => $.execution.clear)}
                aria-label={t(($) => $.execution.parameters)}
              />
            }
          >
            <SlidersHorizontal className="size-3 shrink-0" />
            <span className="truncate">{label}</span>
          </DropdownMenuTrigger>
          <DropdownMenuContent align="start">
            <ChatSettingsMenu settings={settings} includePlan={false} />
          </DropdownMenuContent>
        </DropdownMenu>
      )}
    </>
  );
}
