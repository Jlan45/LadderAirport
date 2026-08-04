# 前端代码编写与设计规范 (Frontend Development & Design Guidelines)

> 本规范涵盖基于 **React 19 + TypeScript + Vite + Tailwind CSS 4** 的 Ladder Airport 前端项目设计系统、组件排版、防抖动准则及代码质量约束。

---

## 1. 设计系统与主题 Token 规范 (Design Tokens & Color Semantics)

为保证在**浅色模式 (Light Mode)** 与 **深色模式 (Dark Mode)** 之间无缝切换且文字高对比度可见，**严禁在业务页面或组件中硬编码单向暗色/亮色类名**。

### 规则：
- **禁止硬编码**: 严禁使用 `bg-zinc-900`, `text-zinc-200`, `text-zinc-400`, `border-zinc-800`, `bg-zinc-950` 等固定颜色类。
- **推荐使用语义 Token**:
  - 主背景 / 卡片背景: `bg-background` / `bg-card`
  - 主文字 / 次要文字: `text-foreground` / `text-muted-foreground`
  - 主边框 / 输入框边框: `border-border` / `border-input`
  - 弱强调背景 / 悬浮态: `bg-muted` / `hover:bg-accent`
  - 品牌主色 / 破坏性动作: `bg-primary text-primary-foreground` / `text-destructive`

---

## 2. 按钮间距与布局规范 (Button & Layout Spacing)

### 按钮组件 (`Button`) 准则：
1. **自带 Flex Gap**: `Button` 基础 Primitive 已内建 `gap-2`（`sm` 尺寸为 `gap-1.5`，`lg` 尺寸为 `gap-2.5`）。在传入图标与文本时**无需手动添加 `mr-2` 或 `ml-1`**。
2. **触感微交互**: 所有交互按钮保持 `transition-all duration-150 active:scale-[0.98] cursor-pointer select-none` 的响应感。

### 按钮组与页脚布局：
- **操作栏按钮组**: 统一采用 Flex gap 排版（`flex items-center gap-2` 或 `gap-2.5`），**禁止使用 `space-x-*`**，以防止换行时边距溢出。
- **弹窗/卡片页脚 (`DialogFooter` / `SheetFooter` / `CardFooter`)**:
  - 统一为 `flex flex-col-reverse sm:flex-row sm:items-center sm:justify-end gap-2.5`。

---

## 3. 防止页面抖动准则 (Preventing Layout Shifts & Jitter)

切换选项卡或动态加载列表时，若出现位置抖动，需按以下规则对齐：

1. **固定滚动条占用 (Scrollbar Gutter)**:
   - 全局 `html` 元素必须声明 `scrollbar-gutter: stable; overflow-y: scroll;`。
   - 防止切换不同高度的选项卡时，浏览器垂直滚动条显隐导致的 6px~15px 整页横向跳动。
2. **边框盒像素对齐 (Box-Sizing Stability)**:
   - 导航项或选项卡在 `active` 状态含有 `border border-border` 时，未激活状态必须含有 `border border-transparent` 占位，保证盒模型物理尺寸完全一致。

---

## 4. 表格与空状态规范 (Table & Empty State Consistency)

### 表格行操作栏：
- 表格右侧操作列统一使用 `flex items-center justify-end gap-1.5`。
- 行内操作按钮统一使用 `size="sm"` (`h-8 px-2 text-xs`) + `variant="ghost"`。

### 空状态 (Empty State)：
- 统一容器: `<Card className="border-border bg-card">`
- 统一布局: `<div className="flex flex-col items-center justify-center py-12 space-y-3 text-center">`
- 统一图标: 居中放置 `<Icon className="h-10 w-10 text-muted-foreground stroke-[1.5]" />`
- 统一文字: 标题 `text-base font-semibold text-foreground` + 副标题 `text-xs text-muted-foreground` + 行内新建按钮。

---

## 5. TypeScript 与代码结构规范 (TypeScript & Architecture)

- **类型安全**: 严格使用 TypeScript 类型声明，禁止使用 `any`。异步 API 请求与 Data Types 需使用 `api/client.ts` 导出的类型。
- **组件分层**:
  - `src/components/ui/`: 基础无状态 UI 零件（Button, Card, Input, Select, Dialog, Sheet, Tabs, Table）。
  - `src/components/`: 业务复用组件与抽屉弹窗（AddNodeModal, NodeDetailDrawer, QRCodeModal）。
  - `src/pages/`: 路由页面组件。
- **构建校验**: 提交代码前确保 `npm run build` (`tsc -b && vite build`) 零错误通过。
