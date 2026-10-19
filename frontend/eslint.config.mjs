// eslint-config-next 16 ships as a native ESLint 9 flat config array.
import nextConfig from "eslint-config-next";
import nextCoreWebVitals from "eslint-config-next/core-web-vitals";

const config = [
  ...nextConfig,
  ...nextCoreWebVitals,
  {
    ignores: [
      "node_modules/**",
      ".next/**",
      "out/**",
      "coverage/**",
      "playwright-report/**",
      "src/lib/api/schema.gen.ts",
    ],
  },
  {
    // UI copy must come from src/messages/*.json (14-frontend §5); numbers and symbols are fine.
    files: ["src/**/*.tsx"],
    rules: {
      "no-restricted-syntax": [
        "error",
        {
          selector: "JSXText[value=/[A-Za-z\\u4e00-\\u9fff]/]",
          message: "Move UI text into src/messages/*.json and use next-intl.",
        },
      ],
    },
  },
];

export default config;
