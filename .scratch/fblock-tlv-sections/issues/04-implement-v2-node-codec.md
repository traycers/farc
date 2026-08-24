# 04: Реализовать кодек TLV-узла и переписать fblock/*.go под v2.0

Status: open

Зависит от: 01 (сигнал завершённости), 02 (область действия), 03 (паддинг) — все три должны быть resolved до начала реализации.

## Задача (когда откроется)

Написать общий encoder/decoder TLV-узла (`magic_start/type/id/parent/sibling/value_size/value/[паддинг]/crc32_header/crc32_value/magic_finish` — точный состав зависит от решений 01/03) и переписать `fblock/prolog.go`, `catalog.go`, `header.go`, `epilog.go` на его основе для формата v2.0 (`03-storage-format.md` §12), с новой мажорной версией (`format_version_major`).

Не начинать без resolved 01–03 — иначе кодек придётся переписывать заново.

## Answer

(не заполнено)
