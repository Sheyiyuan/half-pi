# Half Pi Wiki

Half Pi 的中文官网门户，基于 Astro Starlight 构建并部署到 GitHub Pages。包含产品介绍、架构手册和学习教程，不要求编程基础。

## 本地开发

```bash
cd wiki
pnpm install
pnpm dev
```

默认开发地址为 `http://localhost:4321/`。

## 构建

```bash
cd wiki
pnpm build
pnpm preview
```

生成文件位于 `wiki/dist/`。文档内容放在 `src/content/docs/`，站点导航、Mermaid 转换和 Pages 根路径配置位于 `astro.config.mjs`。

GitHub Pages 构建使用仓库 workflow 注入 `WIKI_BASE=/half-pi`；本地开发和普通构建保持根路径 `/`。
