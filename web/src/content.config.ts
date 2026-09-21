import { defineCollection, z } from 'astro:content';
import { glob } from 'astro/loaders';

const work = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/work' }),
  schema: z.object({
    title: z.string(),
    /** One line, shown in the work list. Keep it concrete. */
    summary: z.string(),
    year: z.number(),
    stack: z.array(z.string()),
    role: z.string().optional(),
    repo: z.string().url().optional(),
    live: z.string().url().optional(),
    /** Lower sorts first on the work index. */
    order: z.number().default(99),
    draft: z.boolean().default(false),
  }),
});

/* Employment history. Separate from `work` on purpose: a job is a span of time
   with an employer attached, a project is a thing that shipped. Forcing both
   through one schema would have made half the fields optional in every entry. */
const experience = defineCollection({
  loader: glob({ pattern: '**/*.md', base: './src/content/experience' }),
  schema: z.object({
    company: z.string(),
    role: z.string(),
    /** `YYYY-MM`. Sorts the list and is formatted for display -- see
        ExperienceRow. Kept as a string rather than a Date so the rendered
        month can never drift by a timezone. */
    start: z.string().regex(/^\d{4}-\d{2}$/, 'expected YYYY-MM'),
    /** Omit for the current role; renders as "Present". */
    end: z.string().regex(/^\d{4}-\d{2}$/, 'expected YYYY-MM').optional(),
    location: z.string(),
    url: z.string().url().optional(),
    stack: z.array(z.string()).default([]),
    draft: z.boolean().default(false),
  }),
});

export const collections = { work, experience };
