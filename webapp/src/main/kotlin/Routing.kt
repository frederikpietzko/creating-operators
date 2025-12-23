package io.kops.webapp

import io.kops.webapp.v1alpha1.Webapp
import io.ktor.server.application.*
import io.ktor.server.html.respondHtml
import io.ktor.server.plugins.autohead.*
import io.ktor.server.routing.get
import io.ktor.server.routing.routing
import kotlinx.html.body
import kotlinx.html.span

fun Application.configureRouting() {
    install(AutoHeadResponse)

    routing {
        get("/") {
            val webapps = kubernetesClient.resources(Webapp::class.java).inAnyNamespace().list()
            call.respondHtml {
                body {
                    for (webapp in webapps.items) {
                        span { +"${webapp.metadata.name} in namespace ${webapp.metadata.namespace}" }
                    }
                }
            }
        }
    }
}
