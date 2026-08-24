# 04: Реализовать кодек TLV-узла/эпилога и переписать fblock/*.go под v2.0

Status: open

Зависимости (01, 02, 03) resolved 2026-08-24 — структура зафиксирована в ADR-023 (Принято) и `03-storage-format.md` §12. Можно приступать к реализации.

## Задача

1. Общий encoder/decoder TLV-узла: `magic_start | type | id | parent | sibling | value_size | value | паддинг-до-alignment | crc32_header | crc32_value | magic_finish` (`03-storage-format.md` §12.3).
2. Кодек эпилога: `magic_start | count | [type,id,offset,size,crc32]×5 | crc32_epilogue | magic_finish` (§12.5), с атомарной O(1) перезаписью после завершения каждого узла.
3. Урезанная фиксированная часть пролога без `params_size`/`catalog_size` (§12.2).
4. Дерево из 5 узлов `root`/`params`/`catalog`/`content`/`toc` (§12.4) — конкретные числовые коды `type`, точные размеры полей (`id`/`offset`/`size` и т.п. в байтах) не зафиксированы ADR-023, выбрать при реализации.
5. Переписать `fblock/prolog.go`, `catalog.go`, `header.go`, `epilog.go` на основе кодеков выше, с новой мажорной версией (`format_version_major = 2`).

Не начато.

## Answer

(не заполнено)
