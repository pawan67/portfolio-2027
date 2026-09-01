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

export const collections = { work };
