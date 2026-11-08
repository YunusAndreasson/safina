import js from "@eslint/js";
import { defineConfig, globalIgnores } from "eslint/config";
import astro from "eslint-plugin-astro";
import globals from "globals";
import tseslint from "typescript-eslint";

export default defineConfig([
  globalIgnores(["dist/**", ".astro/**", "node_modules/**", ".demo/**", ".wrangler/**"]),
  {
    files: ["**/*.{js,mjs}"],
    extends: [js.configs.recommended],
    languageOptions: { globals: globals.node },
  },
  {
    files: ["src/**/*.{ts,astro}"],
    // Astro creates virtual files for lint directives; application scripts live
    // in src/scripts so TypeScript can provide a complete program for them.
    ignores: ["**/*.astro/*.ts"],
    extends: [tseslint.configs.strictTypeChecked],
    languageOptions: {
      globals: globals.browser,
      parserOptions: {
        project: "./tsconfig.json",
        tsconfigRootDir: import.meta.dirname,
        extraFileExtensions: [".astro"],
      },
    },
  },
  ...astro.configs.recommended,
  ...astro.configs["jsx-a11y-strict"],
  {
    files: ["**/*.astro"],
    rules: {
      "astro/no-unsafe-inline-scripts": "error",
      // Safari needs role=list when CSS removes list markers.
      "astro/jsx-a11y/no-redundant-roles": ["error", { ul: ["list"] }],
    },
    languageOptions: {
      parserOptions: {
        parser: tseslint.parser,
        project: "./tsconfig.json",
        tsconfigRootDir: import.meta.dirname,
        extraFileExtensions: [".astro"],
      },
    },
  },
  {
    files: ["src/components/Frame.astro"],
    // The conditional named scroll region needs keyboard focus to pan images.
    rules: {
      "astro/jsx-a11y/no-noninteractive-tabindex": ["error", {
        roles: ["region"], tags: [], allowExpressionValues: true,
      }],
    },
  },
]);
