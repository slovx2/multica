"use client";

import { useEffect, useRef, useState, type KeyboardEvent } from "react";
import { ArrowRight, Check, CircleHelp, X } from "lucide-react";
import { Button } from "@multica/ui/components/ui/button";
import { cn } from "@multica/ui/lib/utils";
import {
  useChatCardDecision,
  type ChatCard,
  type ChatCardQuestion,
} from "@multica/core/chat/planning";
import { useT } from "../../i18n";
import { CHAT_COLUMN, CHAT_GUTTER } from "./chat-column";

interface Draft {
  selected: string[];
  text: string;
  skipped: boolean;
  /** Set once the reader moved past the question (answered or skipped). */
  done: boolean;
}

const EMPTY_DRAFT: Draft = { selected: [], text: "", skipped: false, done: false };
// Long enough to read which row was picked, short enough not to feel modal.
const AUTO_ADVANCE_MS = 180;

function hasAnswer(draft: Draft | undefined): boolean {
  return !!draft && (draft.selected.length > 0 || draft.text.trim() !== "");
}

function answerSummary(question: ChatCardQuestion, draft: Draft | undefined): string {
  if (!draft) return "";
  const labels = question.options
    .filter((option) => draft.selected.includes(option.id))
    .map((option) => option.label);
  const text = draft.text.trim();
  if (text) labels.push(question.free_text.secret ? "••••" : text);
  return labels.join(", ");
}

/**
 * The newest pending question card, answered one question at a time above the
 * composer. Answers stay local until the final submit, which writes one user
 * message through the existing card-decision path; the panel disappears as
 * soon as the card stops being pending.
 */
export function ChatQuestionPanel({
  sessionId,
  card,
  disabled,
}: {
  sessionId: string;
  card: ChatCard;
  disabled?: boolean;
}) {
  const { t } = useT("chat");
  const questions = card.payload.questions ?? [];
  const total = questions.length;
  const [drafts, setDrafts] = useState<Record<string, Draft>>({});
  // `total` is the review step; it only exists for multi-question cards.
  const [step, setStep] = useState(0);
  const [collapsed, setCollapsed] = useState(false);
  const [flashOption, setFlashOption] = useState<string | null>(null);
  const decision = useChatCardDecision(sessionId);
  const submitting = useRef(false);
  const advanceTimer = useRef<ReturnType<typeof setTimeout> | null>(null);
  const optionRefs = useRef<(HTMLButtonElement | null)[]>([]);
  const panelRef = useRef<HTMLElement | null>(null);
  // Latest drafts for the deferred auto-advance, which must act on what the
  // reader has now, not on what they had when they clicked.
  const draftsRef = useRef(drafts);
  const shownStep = useRef(step);
  const inactive = !!disabled || decision.isPending || decision.isSuccess;

  useEffect(() => () => {
    if (advanceTimer.current) clearTimeout(advanceTimer.current);
  }, []);

  // Moving between steps unmounts the focused row. Keep keyboard focus inside
  // the panel (unless it was elsewhere, e.g. the composer) so 1-9 / Enter keep
  // working on the next question.
  useEffect(() => {
    if (shownStep.current === step) return;
    shownStep.current = step;
    const panel = panelRef.current;
    const active = document.activeElement;
    if (!panel || (active && active !== document.body && !panel.contains(active))) return;
    panel
      .querySelector<HTMLElement>('[role="radio"], [role="checkbox"], input, [data-step-primary]')
      ?.focus();
  }, [step]);

  if (total === 0) return null;

  const reviewing = step >= total;
  const question = reviewing ? undefined : questions[step];
  const draft = question ? drafts[question.id] ?? EMPTY_DRAFT : EMPTY_DRAFT;

  function send(action: "answer" | "dismiss", next: Record<string, Draft>) {
    if (inactive || submitting.current) return;
    cancelAutoAdvance();
    submitting.current = true;
    decision.mutate(
      action === "dismiss"
        ? { card_id: card.id, action }
        : {
            card_id: card.id,
            action,
            answers: questions.map((q) => {
              const value = next[q.id];
              const skipped = !value || value.skipped || !hasAnswer(value);
              return {
                question_id: q.id,
                selected_option_ids: skipped ? [] : value.selected,
                text: skipped ? "" : value.text.trim(),
                skipped,
              };
            }),
          },
      { onError: () => { submitting.current = false; } },
    );
  }

  function updateDrafts(next: Record<string, Draft>) {
    draftsRef.current = next;
    setDrafts(next);
  }

  function cancelAutoAdvance() {
    if (advanceTimer.current) clearTimeout(advanceTimer.current);
    advanceTimer.current = null;
    setFlashOption(null);
  }

  function goTo(index: number) {
    cancelAutoAdvance();
    setStep(index);
  }

  function advance(next: Record<string, Draft>) {
    if (step < total - 1) setStep(step + 1);
    else if (total > 1) setStep(total);
    else send("answer", next);
  }

  function commit(patch: Partial<Draft>, move: boolean) {
    if (!question || inactive) return;
    // Any edit supersedes a pending single-choice auto-advance.
    cancelAutoAdvance();
    const next = {
      ...drafts,
      [question.id]: { ...draft, ...patch, ...(move ? { done: true } : {}) },
    };
    updateDrafts(next);
    if (move) advance(next);
    return next;
  }

  function chooseOption(index: number) {
    if (!question || inactive) return;
    const option = question.options[index];
    if (!option) return;
    if (question.selection_mode === "multiple") {
      const selected = draft.selected.includes(option.id)
        ? draft.selected.filter((id) => id !== option.id)
        : [...draft.selected, option.id];
      commit({ selected, skipped: false }, false);
      return;
    }
    // Single choice records and moves on; a typed "other" reply is replaced.
    if (!commit({ selected: [option.id], text: "", skipped: false }, false)) return;
    setFlashOption(option.id);
    advanceTimer.current = setTimeout(() => {
      advanceTimer.current = null;
      setFlashOption(null);
      const current = draftsRef.current;
      const value = current[question.id];
      // Only the choice that scheduled this may move on.
      if (!value || value.selected.length !== 1 || value.selected[0] !== option.id || value.text.trim()) return;
      const next = { ...current, [question.id]: { ...value, done: true } };
      updateDrafts(next);
      advance(next);
    }, AUTO_ADVANCE_MS);
  }

  function goNext() {
    if (!question) return;
    if (!hasAnswer(draft)) return;
    commit({ skipped: false }, true);
  }

  function skipQuestion() {
    commit({ selected: [], text: "", skipped: true }, true);
  }

  function canVisit(index: number): boolean {
    const q = questions[index];
    return index === step || (!!q && !!drafts[q.id]?.done);
  }

  function onKeyDown(event: KeyboardEvent<HTMLElement>) {
    if (event.key === "Escape") {
      event.preventDefault();
      cancelAutoAdvance();
      setCollapsed(true);
      return;
    }
    if (inactive) return;
    const typing = event.target instanceof HTMLInputElement;
    if (typing) {
      if (event.key === "Enter" && !event.nativeEvent.isComposing) {
        event.preventDefault();
        goNext();
      }
      return;
    }
    // Enter on a checkbox row confirms the selection instead of toggling it.
    if (
      event.key === "Enter" &&
      event.target instanceof HTMLElement &&
      event.target.getAttribute("role") === "checkbox"
    ) {
      event.preventDefault();
      goNext();
      return;
    }
    if (question && /^[1-9]$/.test(event.key)) {
      const index = Number(event.key) - 1;
      if (index < question.options.length) {
        event.preventDefault();
        chooseOption(index);
        optionRefs.current[index]?.focus();
      }
      return;
    }
    if (event.key === "ArrowDown" || event.key === "ArrowUp") {
      const rows = optionRefs.current.filter((row): row is HTMLButtonElement => !!row);
      if (rows.length === 0) return;
      event.preventDefault();
      const current = rows.indexOf(document.activeElement as HTMLButtonElement);
      const delta = event.key === "ArrowDown" ? 1 : -1;
      rows[(current + delta + rows.length) % rows.length]?.focus();
      return;
    }
    if (event.key === "ArrowLeft" && step > 0) {
      event.preventDefault();
      goTo(step - 1);
    } else if (event.key === "ArrowRight" && step < total && canVisit(step + 1)) {
      event.preventDefault();
      goTo(step + 1);
    }
  }

  if (collapsed) {
    return (
      <div className={cn(CHAT_GUTTER, "pb-2")}>
        <div className={CHAT_COLUMN}>
          <button
            type="button"
            data-slot="chat-question-collapsed"
            onClick={() => setCollapsed(false)}
            aria-label={t(($) => $.questions.expand)}
            className="flex w-full min-h-9 items-center gap-2 rounded-lg border border-surface-border bg-surface-raised px-3 text-left text-label text-foreground hover:bg-accent pointer-coarse:min-h-11"
          >
            <CircleHelp className="size-4 shrink-0 text-brand" aria-hidden="true" />
            <span className="min-w-0 flex-1 truncate">
              {t(($) => $.questions.collapsed, { count: total })}
            </span>
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className={cn(CHAT_GUTTER, "pb-2")}>
      <section
        ref={panelRef}
        data-slot="chat-question-panel"
        aria-label={t(($) => $.questions.label)}
        onKeyDown={onKeyDown}
        className={cn(
          CHAT_COLUMN,
          "flex max-h-[60vh] flex-col overflow-hidden rounded-lg border border-surface-border bg-surface-raised shadow-[var(--menu-shadow)]",
        )}
      >
        <header className="flex shrink-0 items-start gap-2 border-b border-surface-border px-3 py-2">
          <CircleHelp className="mt-0.5 size-4 shrink-0 text-brand" aria-hidden="true" />
          <div className="min-w-0 flex-1 space-y-1.5">
            <div className="text-caption text-muted-foreground" aria-live="polite">
              {reviewing
                ? t(($) => $.questions.review_title)
                : t(($) => $.questions.progress, { current: step + 1, total })}
            </div>
            {total > 1 && (
              <ol className="flex flex-wrap gap-1" aria-label={t(($) => $.questions.label)}>
                {questions.map((q, index) => {
                  const value = drafts[q.id];
                  const answered = !!value?.done && !value.skipped;
                  const skipped = !!value?.done && value.skipped;
                  const current = index === step;
                  return (
                    <li key={q.id}>
                      <button
                        type="button"
                        data-state={current ? "current" : answered ? "answered" : skipped ? "skipped" : "todo"}
                        aria-current={current ? "step" : undefined}
                        disabled={inactive || !canVisit(index)}
                        title={answered ? answerSummary(q, value) : skipped ? t(($) => $.questions.skipped) : undefined}
                        onClick={() => goTo(index)}
                        className={cn(
                          "inline-flex h-6 max-w-40 items-center gap-1 rounded-full border px-2 text-caption",
                          current
                            ? "border-brand bg-brand/10 text-brand"
                            : answered || skipped
                              ? "border-surface-border text-foreground hover:bg-accent"
                              : "border-transparent bg-muted text-faint-foreground",
                        )}
                      >
                        {answered && <Check className="size-3 shrink-0" aria-hidden="true" />}
                        <span className={cn("truncate", skipped && "line-through")}>{q.header || index + 1}</span>
                      </button>
                    </li>
                  );
                })}
              </ol>
            )}
          </div>
          {total > 1 && (
            <Button
              variant="ghost"
              size="xs"
              className="shrink-0 text-muted-foreground"
              disabled={inactive}
              onClick={() => send("dismiss", drafts)}
            >
              {t(($) => $.questions.skip_all)}
            </Button>
          )}
          <Button
            variant="ghost"
            size="icon-xs"
            className="shrink-0 text-muted-foreground"
            aria-label={t(($) => $.questions.collapse)}
            title={t(($) => $.questions.collapse)}
            onClick={() => {
              cancelAutoAdvance();
              setCollapsed(true);
            }}
          >
            <X aria-hidden="true" />
          </Button>
        </header>

        <div className="min-h-0 flex-1 overflow-y-auto px-3 py-2">
          {question ? (
            <QuestionStep
              key={question.id}
              question={question}
              draft={draft}
              flashOption={flashOption}
              disabled={inactive}
              optionRefs={optionRefs}
              onChoose={chooseOption}
              onText={(text) => commit(
                question.selection_mode === "multiple"
                  ? { text, skipped: false }
                  : { text, selected: text.trim() ? [] : draft.selected, skipped: false },
                false,
              )}
            />
          ) : (
            <ul className="space-y-1" aria-label={t(($) => $.questions.review_title)}>
              {questions.map((q, index) => {
                const value = drafts[q.id];
                const summary = answerSummary(q, value);
                return (
                  <li key={q.id}>
                    <button
                      type="button"
                      disabled={inactive}
                      onClick={() => goTo(index)}
                      className="flex w-full min-h-10 flex-col items-start gap-0.5 rounded-md px-2 py-1.5 text-left hover:bg-accent pointer-coarse:min-h-11"
                    >
                      <span className="text-caption text-muted-foreground">{q.text}</span>
                      <span className={cn("text-body", !summary && "text-faint-foreground")}>
                        {summary || (value?.skipped ? t(($) => $.questions.skipped) : t(($) => $.questions.unanswered))}
                      </span>
                    </button>
                  </li>
                );
              })}
            </ul>
          )}
          {decision.error && (
            <p role="alert" className="pt-2 text-caption text-destructive">{decision.error.message}</p>
          )}
        </div>

        <footer className="flex shrink-0 flex-wrap items-center justify-end gap-2 border-t border-surface-border px-3 py-2">
          {reviewing ? (
            <>
              <Button variant="outline" size="sm" disabled={inactive} onClick={() => goTo(total - 1)}>
                {t(($) => $.questions.back)}
              </Button>
              <Button data-step-primary size="sm" disabled={inactive} aria-busy={decision.isPending} onClick={() => send("answer", drafts)}>
                {t(($) => $.questions.submit)}
              </Button>
            </>
          ) : (
            <>
              {step > 0 && (
                <Button variant="ghost" size="sm" className="mr-auto" disabled={inactive} onClick={() => goTo(step - 1)}>
                  {t(($) => $.questions.prev)}
                </Button>
              )}
              <Button variant="outline" size="sm" disabled={inactive} onClick={skipQuestion}>
                {t(($) => $.questions.skip)}
              </Button>
              <Button size="sm" disabled={inactive || !hasAnswer(draft)} aria-busy={decision.isPending} onClick={goNext}>
                {step < total - 1
                  ? t(($) => $.questions.next)
                  : total > 1
                    ? t(($) => $.questions.review)
                    : t(($) => $.questions.submit)}
              </Button>
            </>
          )}
        </footer>
      </section>
    </div>
  );
}

function QuestionStep({
  question,
  draft,
  flashOption,
  disabled,
  optionRefs,
  onChoose,
  onText,
}: {
  question: ChatCardQuestion;
  draft: Draft;
  flashOption: string | null;
  disabled: boolean;
  optionRefs: React.MutableRefObject<(HTMLButtonElement | null)[]>;
  onChoose: (index: number) => void;
  onText: (text: string) => void;
}) {
  const { t } = useT("chat");
  const multiple = question.selection_mode === "multiple";
  optionRefs.current = [];
  return (
    <div className="space-y-2">
      <p className="text-body font-medium text-foreground">{question.text}</p>
      {multiple && <p className="text-caption text-muted-foreground">{t(($) => $.questions.multi_hint)}</p>}
      {question.options.length > 0 && (
        <div
          role={multiple ? "group" : "radiogroup"}
          aria-label={question.text}
          className="space-y-1"
        >
          {question.options.map((option, index) => {
            const selected = draft.selected.includes(option.id);
            return (
              <button
                key={option.id}
                ref={(node) => { optionRefs.current[index] = node; }}
                type="button"
                role={multiple ? "checkbox" : "radio"}
                aria-checked={selected}
                disabled={disabled}
                onClick={() => onChoose(index)}
                className={cn(
                  "group flex w-full min-h-10 items-center gap-3 rounded-md border px-2.5 py-1.5 text-left outline-none transition-colors pointer-coarse:min-h-11",
                  "focus-visible:ring-2 focus-visible:ring-ring/40",
                  selected || flashOption === option.id
                    ? "border-brand bg-brand/10"
                    : "border-transparent hover:bg-accent focus-visible:bg-accent",
                )}
              >
                <span
                  aria-hidden="true"
                  className={cn(
                    "flex size-5 shrink-0 items-center justify-center rounded-xs text-caption tabular-nums",
                    selected ? "bg-brand text-brand-foreground" : "bg-muted text-muted-foreground",
                  )}
                >
                  {selected && multiple ? <Check className="size-3" /> : index + 1}
                </span>
                <span className="min-w-0 flex-1">
                  <span className="block text-body text-foreground">{option.label}</span>
                  {option.description && (
                    <span className="block text-caption text-muted-foreground">{option.description}</span>
                  )}
                </span>
                <ArrowRight
                  aria-hidden="true"
                  className="size-4 shrink-0 text-muted-foreground opacity-0 group-hover:opacity-100 group-focus-visible:opacity-100"
                />
              </button>
            );
          })}
        </div>
      )}
      {question.free_text.allowed && (
        <input
          className="h-10 w-full rounded-md border border-surface-border bg-surface px-2.5 text-body outline-none focus-visible:border-brand pointer-coarse:h-11"
          type={question.free_text.secret ? "password" : "text"}
          autoComplete="off"
          disabled={disabled}
          aria-label={t(($) => $.questions.other_placeholder)}
          placeholder={t(($) => $.questions.other_placeholder)}
          value={draft.text}
          onChange={(event) => onText(event.target.value)}
        />
      )}
    </div>
  );
}
