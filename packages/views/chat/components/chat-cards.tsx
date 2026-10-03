"use client";

import { useRef, useState } from "react";
import { Button } from "@multica/ui/components/ui/button";
import { Markdown } from "@multica/ui/markdown";
import { useChatCards, useChatCardDecision, type ChatCard, type CardDecision } from "@multica/core/chat/planning";
import { useT } from "../../i18n";

export function ChatCards({ sessionId, disabled }: { sessionId: string | null; disabled?: boolean }) {
  const { data = [], error } = useChatCards(sessionId);
  if (!sessionId) return null;
  return <div className="max-h-[40%] shrink-0 overflow-auto space-y-3 px-3">{error && <p role="alert">{error.message}</p>}
    {data.map((card) => <ChatInteractionCard key={card.id} sessionId={sessionId} card={card} disabled={disabled} />)}
  </div>;
}

export function ChatInteractionCard({ sessionId, card, disabled }: { sessionId: string; card: ChatCard; disabled?: boolean }) {
  const { t } = useT("chat");
  const [feedback, setFeedback] = useState("");
  const [answers, setAnswers] = useState<Record<string, { selected: string[]; text: string }>>({});
  const decision = useChatCardDecision(sessionId);
  const submitting = useRef(false);
  const inactive = disabled || card.status !== "pending" || decision.isPending || decision.isSuccess;
  function answerFor(id: string) {
    const saved = card.response?.answers?.find((answer) => answer.question_id === id);
    return saved ? { selected: saved.selected_option_ids, text: saved.text } : answers[id];
  }
  function submit(action: CardDecision["action"]) {
    if (inactive || submitting.current) return;
    submitting.current = true;
    decision.mutate({ card_id: card.id, action, feedback,
      answers: card.payload.questions?.map((q) => ({ question_id: q.id, selected_option_ids: answers[q.id]?.selected ?? [], text: answers[q.id]?.text ?? "" })),
    }, { onError: () => { submitting.current = false; } });
  }
  const statusLabels = {
    pending: t(($) => $.planning.pending), approved: t(($) => $.planning.approved),
    rejected: t(($) => $.planning.rejected), superseded: t(($) => $.planning.superseded), answered: t(($) => $.planning.answered),
  };
  return <section className="space-y-3 rounded-lg border border-surface-border bg-surface-raised p-3" aria-label={card.payload.title}>
    <div className="flex justify-between gap-2 text-caption"><strong>{card.payload.title}</strong><span>{statusLabels[card.status]}</span></div>
    {card.kind === "plan" ? <>
      <div className="max-h-96 overflow-auto"><Markdown>{card.payload.markdown ?? ""}</Markdown></div>
      {card.status === "pending" && <>
        <textarea className="w-full rounded-md border p-2 text-body-sm" aria-label={t(($) => $.planning.feedback)} placeholder={t(($) => $.planning.feedback)} value={feedback} disabled={inactive} onChange={(e) => setFeedback(e.target.value)} />
        <div className="flex flex-wrap gap-2"><Button size="sm" disabled={inactive} onClick={() => submit("approve")}>{t(($) => $.planning.approve)}</Button>
          <Button size="sm" variant="outline" disabled={inactive} onClick={() => submit("reject")}>{t(($) => $.planning.reject)}</Button></div>
      </>}
    </> : <>
      {card.payload.questions?.map((q) => <fieldset key={q.id} disabled={inactive} className="space-y-2">
        <legend className="text-body-sm font-medium">{q.header}: {q.text}</legend>
        {q.options.map((option) => <label key={option.id} className="flex items-start gap-2 text-body-sm">
          <input type={q.selection_mode === "multiple" ? "checkbox" : "radio"} name={`${card.id}-${q.id}`} checked={answerFor(q.id)?.selected.includes(option.id) ?? false}
            onChange={(e) => setAnswers((old) => ({ ...old, [q.id]: { text: old[q.id]?.text ?? "", selected: q.selection_mode === "multiple" ? (e.target.checked ? [...(old[q.id]?.selected ?? []), option.id] : (old[q.id]?.selected ?? []).filter((id) => id !== option.id)) : [option.id] } }))} />
          <span>{option.label}{option.description && <span className="block text-caption text-muted-foreground">{option.description}</span>}</span>
        </label>)}
        {q.free_text.allowed && <input className="w-full rounded-md border p-2 text-body-sm" type={q.free_text.secret ? "password" : "text"} autoComplete="off" aria-label={`${q.header}: ${t(($) => $.planning.free_text)}`} placeholder={t(($) => $.planning.free_text)} value={answerFor(q.id)?.text ?? ""} onChange={(e) => setAnswers((old) => ({ ...old, [q.id]: { selected: old[q.id]?.selected ?? [], text: e.target.value } }))} />}
      </fieldset>)}
      {card.status === "pending" && <Button size="sm" disabled={inactive} onClick={() => submit("answer")}>{t(($) => $.planning.answer)}</Button>}
    </>}
    {card.status !== "pending" && card.response?.feedback && <p className="text-body-sm">{card.response.feedback}</p>}
    {decision.error && <p role="alert" className="text-caption text-destructive">{decision.error.message}</p>}
  </section>;
}
