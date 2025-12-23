package com.github.frederikpietzko

import ai.koog.ktor.Koog
import ai.koog.ktor.aiAgent
import ai.koog.prompt.executor.clients.openai.OpenAIModels
import com.sksamuel.cohort.*
import com.sksamuel.cohort.cpu.*
import com.sksamuel.cohort.memory.*
import io.ktor.http.*
import io.ktor.serialization.kotlinx.json.*
import io.ktor.server.application.*
import io.ktor.server.plugins.autohead.*
import io.ktor.server.plugins.callid.*
import io.ktor.server.plugins.calllogging.*
import io.ktor.server.plugins.contentnegotiation.*
import io.ktor.server.request.*
import io.ktor.server.response.*
import io.ktor.server.routing.*
import kotlin.time.Duration.Companion.seconds
import kotlinx.coroutines.Dispatchers
import org.slf4j.event.*

fun Application.configureRouting() {
    install(AutoHeadResponse)
}
