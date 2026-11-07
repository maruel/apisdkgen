// Exercises typed URL queries independently of JSON bodies and SSE responses.
package querycheck

import kotlinx.coroutines.flow.first
import kotlinx.coroutines.runBlocking

fun main(args: Array<String>) = runBlocking {
    val http = okhttp3.OkHttpClient()
    val client = ApiClient(baseURL = args[0], httpClient = http)
    try {
        val query = testQueryInput(limit = 0, unread = false, text = "a +&雪", ids = "a,b", maybe = null, extra = "ignored")
        check(client.list("full", query).kind == "listed")
        check(client.list("blank", query.copy(text = "")).kind == "listed")
        check(client.list("empty", testQueryInput(extra = "ignored")).kind == "listed")
        check(client.update(query, testSDKEvent(kind = "request"), mapOf("X-Test" to "expected")).kind == "listed")
        check(client.events(query).first().kind == "listed")
        check(client.eventsReconnecting(query).first().kind == "listed")
        check(client.namedEvents(query).first().kind == "listed")
        check(client.namedEventsReconnecting(query).first().kind == "listed")
    } finally {
        http.dispatcher.executorService.shutdown()
        http.connectionPool.evictAll()
    }
}
