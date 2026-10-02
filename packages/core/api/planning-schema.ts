import { z } from "zod";
const questionSchema = z.object({
  id: z.string(), header: z.string(), text: z.string(),
  selection_mode: z.enum(["single", "multiple", "none"]),
  options: z.array(z.object({ id: z.string(), label: z.string(), description: z.string().default("") })),
  free_text: z.object({ allowed: z.boolean(), secret: z.boolean() }),
  required: z.boolean().default(true),
});
const chatCardSchema = z.object({
  id: z.string(), task_id: z.string(),
  kind: z.enum(["plan", "user_question"]),
  status: z.enum(["pending", "answered", "approved", "rejected", "superseded"]).catch("superseded"),
  payload: z.object({
    title: z.string(), markdown: z.string().optional(),
    questions: z.array(questionSchema).optional(),
  }),
  response: z.object({
    action: z.enum(["approve", "reject", "answer"]),
    feedback: z.string().optional(),
    answers: z.array(z.object({ question_id: z.string(), selected_option_ids: z.array(z.string()).default([]), text: z.string().default("") })).optional(),
  }).nullish(),
});
export const chatCardsSchema = z.array(chatCardSchema.nullable().catch(null))
  .transform((cards) => cards.filter((card): card is z.infer<typeof chatCardSchema> => card !== null));
export type ChatCard = z.infer<typeof chatCardsSchema>[number];
export interface CardDecision {
  card_id: string;
  action: "approve" | "reject" | "answer";
  feedback?: string;
  answers?: { question_id: string; selected_option_ids: string[]; text: string }[];
}


export const planningLinksSchema = z.array(z.object({ id: z.string(), title: z.string().default("") }));
export type PlanningLink = z.infer<typeof planningLinksSchema>[number];
