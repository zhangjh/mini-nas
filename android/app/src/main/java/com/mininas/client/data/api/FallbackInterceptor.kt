package com.mininas.client.data.api

import okhttp3.Interceptor
import okhttp3.Response
import java.io.IOException

class FallbackInterceptor(
    private val urlSelector: ServerUrlSelector,
    private val apiProvider: ApiProvider,
) : Interceptor {

    override fun intercept(chain: Interceptor.Chain): Response {
        try {
            return chain.proceed(chain.request())
        } catch (e: IOException) {
            val currentUrl = urlSelector.currentSync()
            val lanUrl = urlSelector.lanUrlSync()
            
            if (lanUrl.isNotBlank() && currentUrl == ApiProvider.normalize(lanUrl)) {
                val publicUrl = urlSelector.publicUrlSync()
                if (publicUrl.isNotBlank()) {
                    apiProvider.updateBaseUrl(publicUrl)
                    urlSelector.markLanUnreachable()
                    
                    val newRequest = chain.request().newBuilder()
                        .url(chain.request().url.toString().replace(
                            ApiProvider.normalize(lanUrl),
                            ApiProvider.normalize(publicUrl)
                        ))
                        .build()
                    return chain.proceed(newRequest)
                }
            }
            throw e
        }
    }
}
