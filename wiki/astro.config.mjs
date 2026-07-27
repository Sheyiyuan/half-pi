// @ts-check
import { defineConfig } from 'astro/config';
import starlight from '@astrojs/starlight';
import { unified } from '@astrojs/markdown-remark';

const base = process.env.WIKI_BASE ?? '/';

function escapeHTML(value) {
	return value
		.replaceAll('&', '&amp;')
		.replaceAll('<', '&lt;')
		.replaceAll('>', '&gt;');
}

function remarkMermaid() {
	return (tree) => {
		const visit = (node) => {
			if (node.type === 'code' && node.lang === 'mermaid') {
				node.type = 'html';
				node.value = `<pre class="mermaid" aria-label="架构图">${escapeHTML(node.value)}</pre>`;
				delete node.lang;
				delete node.meta;
				return;
			}
			for (const child of node.children ?? []) visit(child);
		};
		visit(tree);
	};
}

/**
 * 把每个表格包进可横向滚动的容器。
 * 表格本身撑满正文列（表头底色才不会参差不齐），溢出交给外层容器，
 * 避免窄屏下表格把整个 document 顶宽、页面左右边距全部跑偏。
 */
function rehypeTableScroll() {
	return (tree) => {
		const visit = (node) => {
			const children = node.children;
			if (!children) return;
			for (let i = 0; i < children.length; i += 1) {
				const child = children[i];
				if (child.type === 'element' && child.tagName === 'table') {
					children[i] = {
						type: 'element',
						tagName: 'div',
						properties: { className: ['table-scroll'] },
						children: [child],
					};
					visit(child);
					continue;
				}
				visit(child);
			}
		};
		visit(tree);
	};
}

export default defineConfig({
	site: 'https://sheyiyuan.github.io',
	base,
	markdown: {
		processor: unified({
			remarkPlugins: [remarkMermaid],
			rehypePlugins: [rehypeTableScroll],
		}),
	},
	integrations: [
		starlight({
			title: 'Half Pi · 半派',
			description: '自托管、跨设备、始终在线的个人 AI 助理。产品介绍、架构手册与学习教程。',
			logo: {
				src: './src/assets/logo.svg',
				alt: 'Half Pi 标志',
			},
			favicon: '/favicon.svg',
			locales: {
				root: { label: '简体中文', lang: 'zh-CN' },
			},
			customCss: ['./src/styles/custom.css'],
			components: {
				Head: './src/components/Head.astro',
				Footer: './src/components/Footer.astro',
			},
			social: [
				{
					icon: 'github',
					label: 'Half Pi on GitHub',
					href: 'https://github.com/Sheyiyuan/half-pi',
				},
			],
			sidebar: [
				{ label: '首页', link: '/' },
				{
					label: '学习教程',
					items: [{ autogenerate: { directory: 'tutorial' } }],
				},
				{
					label: '架构手册',
					items: [{ autogenerate: { directory: 'architecture' } }],
				},
				{ label: '术语表', link: '/glossary/' },
			],
		}),
	],
});
