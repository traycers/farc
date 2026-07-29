# Дерево данных: каналы, потоки, кадры

## 1. Назначение документа

Документ конкретизирует дерево (`05-data-format.md`) для доменной модели farc: разбиение данных фконтейнера на каналы, потоки (видео/аудио), версии параметров кодека и кадры (I/P, GOP). Определяет набор значений `role`, соответствующий им `type` (физическое представление `value`) и форму `value` для каждой роли.

Документ **не описывает**:
- Общий формат элемента дерева (`type`, `role`, `parent`, `sibling`, `size`, `value`/`offset`) — см. `05-data-format.md`.
- Формат TOC — см. `06-toc-format.md`.
- Формат самих кодек-параметров и кадров (SPS/PPS, ASC и т.п.) — специфика кодека, вне области farc.

## 2. Иерархия

```
/root
/root/channels
/root/channels/id
/root/channels/id/streams
/root/channels/id/streams/id
/root/channels/id/streams/id/{video,audio}/params
/root/channels/id/streams/id/{video,audio}/params/id
/root/channels/id/streams/id/{video,audio}/params/id/data      // сами параметры кодека
/root/channels/id/streams/id/{video,audio}/params/id/frames
/root/channels/id/streams/id/{video,audio}/params/id/frames/frame        // кадр (видео и звук — общий контейнер)
/root/channels/id/streams/id/{video,audio}/params/id/frames/frame/data   // байты кадра
/root/channels/id/streams/id/{video,audio}/params/id/frames/frame/time   // время кадра
/root/channels/id/streams/id/{video,audio}/params/id/frames/frame/kind   // video: I/P; audio: кодек (pcm/aac/g.711)
```

`id` в пути — доменный идентификатор (номер канала, номер потока, номер версии параметров), не путать с `id` узла дерева (§4).

Та же иерархия в виде схемы (ветка `audio` зеркальна ветке `video`, показана свёрнуто):

```mermaid
flowchart TD
    root(("root"))
    channels["channels"]
    channel["channel (×N)"]
    streams["streams"]
    stream["stream (×N)"]
    video["video (0..1)"]
    audio["audio (0..1)"]

    vparams["params"]
    vver["params_version (×N)"]
    vdata["data"]
    vframes["frames"]
    vframe["frame (×N)"]
    vfdata["frame_data"]
    vftime["frame_time"]
    vfkind["frame_kind_video (0..1)"]

    aparams["params"]
    aver["params_version (×N)"]
    adata["data"]
    aframes["frames"]
    aframe["frame (×N)"]
    afdata["frame_data"]
    aftime["frame_time"]
    afcodec["frame_codec_audio (0..1)"]

    root --> channels --> channel --> streams --> stream
    stream --> video
    stream --> audio

    video --> vparams --> vver
    vver --> vdata
    vver --> vframes
    vframes --> vframe
    vframe --> vfdata
    vframe --> vftime
    vframe --> vfkind

    audio --> aparams --> aver
    aver --> adata
    aver --> aframes
    aframes --> aframe
    aframe --> afdata
    aframe --> aftime
    aframe --> afcodec
```

`frame` — общий контейнер для видео и звука; у звука нет разбиения на ключевые/неключевые кадры, поэтому дерево для обеих веток одинаковое по форме, различается только роль узла-«вида» (`frame_kind_video` против `frame_codec_audio`, см. §3). Порядок `frame` под одним `frames` — порядок их `id` при сканировании `parent == frames` (`05-data-format.md` §3), совпадающий с физическим порядком создания; для видео он же — порядок декодирования, включая P-кадры внутри GOP.

## 3. Роли узлов

`type` (`05-data-format.md` §3.1) — закрытый набор физических представлений `value`, общий для всего формата, не специфичный для домена. В медиа-дереве встречаются: `void` (нет `value` — контейнер/тег ветки), `uint8`, `uint32`, `uint64`, `bytes`. Отдельного enum-типа нет (как в EBML): перечислимые значения (`frame_kind_video`, `frame_codec_audio`) физически хранятся как `uint8`, смысл конкретных значений задаёт `role`, а не `type` (`05-data-format.md` §3.1).

| `role`              | `type`   | Родитель          | Кратность у родителя | `value` |
|---------------------|----------|-------------------|-----------------------|---------|
| `root`              | `void`   | сам себе          | 1 (корень дерева)      | — |
| `channels`          | `void`   | `root`            | 1                      | — (контейнер) |
| `channel`           | `uint32` | `channels`        | N                      | номер канала |
| `streams`           | `void`   | `channel`         | 1                      | — (контейнер) |
| `stream`            | `uint32` | `streams`         | N                      | номер потока |
| `video`             | `void`   | `stream`          | 0..1                   | — (тег ветки) |
| `audio`             | `void`   | `stream`          | 0..1                   | — (тег ветки) |
| `params`            | `void`   | `video` / `audio` | 1                      | — (контейнер) |
| `params_version`    | `uint64` | `params`          | N                      | — (см. §5) |
| `data`              | `bytes`  | `params_version`  | 1                      | параметры кодека |
| `frames`            | `void`   | `params_version`  | 1                      | — (контейнер) |
| `frame`             | `void`   | `frames`          | N                      | — (контейнер, атрибуты в детях) |
| `frame_data`        | `bytes`  | `frame`           | 1                      | байты кадра |
| `frame_time`        | `uint64` | `frame`           | 1                      | время кадра (uint64, Unix наносекунды) |
| `frame_kind_video`  | `uint8`  | `frame` (только video) | 0..1              | `I` (ключевой кадр) или `P` |
| `frame_codec_audio` | `uint8`  | `frame` (только audio) | 0..1              | код кодека (`pcm`/`aac`/`g711`) |

Несколько ролей намеренно делят один `type` (`params_version`/`frame_time` — обе `uint64`; `frame_kind_video`/`frame_codec_audio` — обе `uint8`) — это не проблема, а расчётный эффект разделения полей: `type` описывает только физическую раскладку `value`, семантику несёт `role`.

`video`/`audio` — не контейнеры и не повторяются: у потока не более одного узла каждого вида; отсутствие узла означает отсутствие соответствующей дорожки в потоке.

`frame_data` (а не `data`) и раздельные `frame_kind_video`/`frame_codec_audio` (а не общая роль `kind`) — намеренно уникальные роли, а не переиспользование `data`/общей роли вида: одна и та же `role` не должна означать разные вещи в разных ветках дерева (иначе подсчёт/индексация по `role` становится неоднозначной, см. `06-toc-format.md`). Роль узла сама по себе однозначно определяет ветку и семантику — доп. поле-скоуп не нужно.

## 4. Доменные идентификаторы vs id узла дерева

`id` узла (см. `05-data-format.md` §3) — позиция элемента при создании, не переиспользуется и не несёт смысла для потребителя. Номер канала, номер потока и номер версии параметров — доменные идентификаторы, которыми оперирует потребитель (например, «открыть канал 7»); они хранятся в `value` соответствующего узла (`channel`, `stream`), а не в `parent`/`sibling`.

Поиск узла по доменному идентификатору (например, канала 7 среди детей `channels`) выполняется сканированием `parent == channels` с проверкой `value` — количество каналов/потоков на фконтейнер невелико (см. ADR-014, регистр каналов), поэтому линейный скан не является проблемой производительности.

## 5. Заполнение узлов

- **`data`** — параметры кодека (например, SPS/PPS для H.264, ASC для AAC), непрозрачны для farc.
- **`frame`** — контейнер; `value` не используется. Атрибуты кадра — в дочерних узлах ниже.
- **`frame_data`** — закодированные байты кадра (ключевого или нет — см. `frame_kind_video`).
- **`frame_time`** — `value` — абсолютное время кадра, uint64, Unix-время в наносекундах (соглашение бинарного формата, `03-storage-format.md` §2; источник — микросекундная точность, младшие 3 разряда всегда нулевые).
- **`frame_kind_video`** — только для video-ветки; `value` — маркер `I` (ключевой кадр, начало новой GOP) или `P` (не ключевой, той же GOP).
- **`frame_codec_audio`** — только для audio-ветки; `value` — идентификатор кодека кадра (`pcm`/`aac`/`g711`).
- **`params_version`** — фиксирует момент смены параметров кодека внутри потока (например, смена разрешения); `value` — момент смены, uint64, тот же формат времени, что и у `frame_time`.

## 6. Открытые вопросы

- Точный формат доменного идентификатора в `value` узлов `channel`/`stream` (просто число фиксированного размера, или с дополнительными полями) — согласовать с `00-requirements.md`/ADR-014, где уже определён номер канала.
- Физический размер `value` у `frame_kind_video` и `frame_codec_audio` решён (`uint8`, `05-data-format.md` §3.1); конкретное числовое значение для каждого варианта (`I`/`P`; `pcm`/`aac`/`g711`) ещё не зафиксировано.
- ~~Связь P-кадра со своим ключевым кадром GOP~~ — решено: P и I лежат на одном уровне, оба как плоские дети `frames`; связь раньше выражалась через `parent` (`frame_p`, родитель — `frame_i`), теперь видовой признак I/P — атрибут `frame_kind_video`. Восстановление GOP — сканирование цепочки `sibling` назад до ближайшего `frame` с `frame_kind_video = I` (декодер всегда идёт последовательно). Произвольный доступ к «своему» I-кадру без обхода соседей не требуется — дополнительная ссылка не нужна.
